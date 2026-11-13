package complete

import (
	"bufio"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/typemap"
)

// defaultName is the name of the default AWS profile and of the default workspace.
const defaultName = "default"

// Profiles lists the profile names in the AWS shared config file: AWS_CONFIG_FILE, else
// ~/.aws/config. It never reads the credentials file. A missing file lists nothing.
func Profiles(lookup func(string) (string, bool)) Source {
	return func(ctx context.Context, _ *cobra.Command, _ string) ([]Choice, error) {
		path := awsConfigPath(lookup)
		if path == "" {
			return nil, nil
		}
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("open aws config: %w", err)
		}
		defer func() { _ = f.Close() }()
		return profileSections(ctx, f)
	}
}

func awsConfigPath(lookup func(string) (string, bool)) string {
	if p, ok := lookup("AWS_CONFIG_FILE"); ok && p != "" {
		return p
	}
	for _, k := range []string{"HOME", "USERPROFILE"} {
		if home, ok := lookup(k); ok && home != "" {
			return filepath.Join(home, ".aws", "config")
		}
	}
	return ""
}

// profileSections reads "[default]" and "[profile name]" headers and skips other section kinds,
// such as sso-session and services.
func profileSections(ctx context.Context, r io.Reader) ([]Choice, error) {
	var out []Choice
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("read aws config: %w", err)
		}
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line[1 : len(line)-1]))
		var name string
		switch {
		case len(fields) == 1 && fields[0] == defaultName:
			name = defaultName
		case len(fields) == 2 && fields[0] == "profile":
			name = fields[1]
		default:
			continue
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, Choice{Value: name})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read aws config: %w", err)
	}
	slices.SortFunc(out, func(a, b Choice) int { return cmp.Compare(a.Value, b.Value) })
	return out, nil
}

// Units lists the unit paths in the state index file named by the indexFlag flag, from
// tfcensus inventory --save-index. Without that flag it lists nothing.
func Units(indexFlag string) Source {
	return func(ctx context.Context, cmd *cobra.Command, _ string) ([]Choice, error) {
		path, _ := cmd.Flags().GetString(indexFlag)
		if path == "" {
			return nil, nil
		}
		var env struct {
			Data struct {
				Units []struct {
					Unit      string `json:"unit"`
					Workspace string `json:"workspace"`
					Status    string `json:"status"`
					Instances int    `json:"instances"`
				} `json:"units"`
			} `json:"data"`
		}
		if err := readIndex(ctx, path, &env); err != nil {
			return nil, err
		}
		var out []Choice
		seen := map[string]bool{}
		for _, u := range env.Data.Units {
			if u.Unit == "" || seen[u.Unit] {
				continue
			}
			seen[u.Unit] = true
			desc := u.Status + ", " + strconv.Itoa(u.Instances) + " instance"
			if u.Instances != 1 {
				desc += "s"
			}
			if u.Workspace != "" && u.Workspace != defaultName {
				desc += ", workspace " + u.Workspace
			}
			out = append(out, Choice{Value: u.Unit, Desc: desc})
		}
		slices.SortFunc(out, func(a, b Choice) int { return cmp.Compare(a.Value, b.Value) })
		return out, nil
	}
}

// readIndex decodes an index file like tfstate.ReadIndexFile does. tfstate also reads states
// from S3, so this package keeps its own copy to stay off the network.
func readIndex(ctx context.Context, path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open index file: %w", err)
	}
	defer func() { _ = f.Close() }()
	var r io.Reader = f
	switch {
	case strings.HasSuffix(path, ".zst"):
		zr, zerr := zstd.NewReader(f)
		if zerr != nil {
			return fmt.Errorf("open index file: %w", zerr)
		}
		defer zr.Close()
		r = zr
	case strings.HasSuffix(path, ".gz"):
		gr, gerr := gzip.NewReader(f)
		if gerr != nil {
			return fmt.Errorf("open index file: %w", gerr)
		}
		defer func() { _ = gr.Close() }()
		r = gr
	}
	if err = json.NewDecoder(ctxReader{ctx, r}).Decode(v); err != nil {
		return fmt.Errorf("decode index file: %w", err)
	}
	return nil
}

// ctxReader stops reading once ctx is done, so a timed-out completion doesn't keep decoding.
type ctxReader struct {
	ctx context.Context //nolint:containedctx // bounds a single Read loop
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, fmt.Errorf("read index file: %w", err)
	}
	return c.r.Read(p) //nolint:wrapcheck // io.EOF must reach the decoder unwrapped
}

// TFTypes lists the TF resource types in the embedded typemap, with their CloudFormation types
// as descriptions.
func TFTypes() Source {
	return func(context.Context, *cobra.Command, string) ([]Choice, error) {
		m, err := typemap.Load()
		if err != nil {
			return nil, fmt.Errorf("load typemap: %w", err)
		}
		var out []Choice
		for _, tf := range m.TFTypes() {
			t, _ := m.Type(tf)
			desc := strings.Join(t.CFN, ", ")
			if desc == "" {
				desc = t.GenResource.CFN
			}
			out = append(out, Choice{Value: tf, Desc: desc})
		}
		return out, nil
	}
}

// CFNTypes lists the CloudFormation types in the embedded typemap, with their TF types as
// descriptions, plus one "AWS::Service::*" glob per service.
func CFNTypes() Source {
	return func(context.Context, *cobra.Command, string) ([]Choice, error) {
		m, err := typemap.Load()
		if err != nil {
			return nil, fmt.Errorf("load typemap: %w", err)
		}
		var out []Choice
		services := map[string]bool{}
		for _, cfn := range m.CFNTypes() {
			var tfs []string
			for _, t := range m.ForCFN(cfn) {
				tfs = append(tfs, t.TF)
			}
			out = append(out, Choice{Value: cfn, Desc: strings.Join(tfs, ", ")})
			if parts := strings.Split(cfn, "::"); len(parts) == 3 && !services[parts[1]] {
				services[parts[1]] = true
				out = append(
					out,
					Choice{Value: parts[0] + "::" + parts[1] + "::*", Desc: "every " + parts[1] + " type"},
				)
			}
		}
		slices.SortFunc(out, func(a, b Choice) int { return cmp.Compare(a.Value, b.Value) })
		return out, nil
	}
}

// Types lists TF types when toComplete starts with a lowercase letter and CloudFormation types
// otherwise, for flags that take either.
func Types() Source {
	tf, cfn := TFTypes(), CFNTypes()
	return func(ctx context.Context, cmd *cobra.Command, toComplete string) ([]Choice, error) {
		if toComplete != "" && toComplete[0] >= 'a' && toComplete[0] <= 'z' {
			return tf(ctx, cmd, toComplete)
		}
		return cfn(ctx, cmd, toComplete)
	}
}

// Regions is the list of AWS commercial regions, with their names.
func Regions() []Choice {
	return []Choice{
		{"af-south-1", "Africa (Cape Town)"},
		{"ap-east-1", "Asia Pacific (Hong Kong)"},
		{"ap-east-2", "Asia Pacific (Taipei)"},
		{"ap-northeast-1", "Asia Pacific (Tokyo)"},
		{"ap-northeast-2", "Asia Pacific (Seoul)"},
		{"ap-northeast-3", "Asia Pacific (Osaka)"},
		{"ap-south-1", "Asia Pacific (Mumbai)"},
		{"ap-south-2", "Asia Pacific (Hyderabad)"},
		{"ap-southeast-1", "Asia Pacific (Singapore)"},
		{"ap-southeast-2", "Asia Pacific (Sydney)"},
		{"ap-southeast-3", "Asia Pacific (Jakarta)"},
		{"ap-southeast-4", "Asia Pacific (Melbourne)"},
		{"ap-southeast-5", "Asia Pacific (Malaysia)"},
		{"ap-southeast-6", "Asia Pacific (New Zealand)"},
		{"ap-southeast-7", "Asia Pacific (Thailand)"},
		{"ca-central-1", "Canada (Central)"},
		{"ca-west-1", "Canada West (Calgary)"},
		{"eu-central-1", "Europe (Frankfurt)"},
		{"eu-central-2", "Europe (Zurich)"},
		{"eu-north-1", "Europe (Stockholm)"},
		{"eu-south-1", "Europe (Milan)"},
		{"eu-south-2", "Europe (Spain)"},
		{"eu-west-1", "Europe (Ireland)"},
		{"eu-west-2", "Europe (London)"},
		{"eu-west-3", "Europe (Paris)"},
		{"il-central-1", "Israel (Tel Aviv)"},
		{"me-central-1", "Middle East (UAE)"},
		{"me-south-1", "Middle East (Bahrain)"},
		{"mx-central-1", "Mexico (Central)"},
		{"sa-east-1", "South America (Sao Paulo)"},
		{"us-east-1", "US East (N. Virginia)"},
		{"us-east-2", "US East (Ohio)"},
		{"us-west-1", "US West (N. California)"},
		{"us-west-2", "US West (Oregon)"},
	}
}
