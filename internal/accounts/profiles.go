package accounts

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Profile is one profile from the shared config or credentials file.
type Profile struct {
	Name       string `json:"name"`
	SSO        bool   `json:"sso,omitempty"`         // credentials come from IAM Identity Center
	SSOSession string `json:"sso_session,omitempty"` // the [sso-session] the profile uses
}

// Files locates the shared AWS config and credentials files.
type Files struct {
	Config      string
	Credentials string
}

// FilesFromEnv returns the files the AWS CLI reads: AWS_CONFIG_FILE and
// AWS_SHARED_CREDENTIALS_FILE, else ~/.aws/config and ~/.aws/credentials.
func FilesFromEnv(lookup func(string) (string, bool)) Files {
	home, _ := lookup("HOME")
	if home == "" {
		home, _ = lookup("USERPROFILE")
	}
	f := Files{
		Config:      filepath.Join(home, ".aws", "config"),
		Credentials: filepath.Join(home, ".aws", "credentials"),
	}
	if p, ok := lookup("AWS_CONFIG_FILE"); ok && p != "" {
		f.Config = p
	}
	if p, ok := lookup("AWS_SHARED_CREDENTIALS_FILE"); ok && p != "" {
		f.Credentials = p
	}
	return f
}

// ReadProfiles lists the profiles in both files, sorted by name. Missing files are empty.
func ReadProfiles(f Files) ([]Profile, error) {
	byName := map[string]*Profile{}
	if err := scanINI(f.Config, true, byName); err != nil {
		return nil, err
	}
	if err := scanINI(f.Credentials, false, byName); err != nil {
		return nil, err
	}
	out := make([]Profile, 0, len(byName))
	for _, p := range byName {
		out = append(out, *p)
	}
	slices.SortFunc(out, func(a, b Profile) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// scanINI reads profile sections. The config file names them "[profile x]" (except
// "[default]"); the credentials file names them "[x]".
func scanINI(file string, isConfig bool, byName map[string]*Profile) error {
	fh, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read aws config: %w", err)
	}
	defer func() { _ = fh.Close() }()
	var cur *Profile
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			cur = profileSection(strings.TrimSpace(line[1:len(line)-1]), isConfig, byName)
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || cur == nil {
			continue
		}
		switch strings.TrimSpace(key) {
		case "sso_session":
			cur.SSO, cur.SSOSession = true, strings.TrimSpace(value)
		case "sso_start_url":
			cur.SSO = true
		}
	}
	if err = sc.Err(); err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	return nil
}

func profileSection(section string, isConfig bool, byName map[string]*Profile) *Profile {
	name := section
	if isConfig {
		rest, ok := strings.CutPrefix(section, "profile ")
		switch {
		case ok:
			name = strings.TrimSpace(rest)
		case section != string(KindDefault):
			return nil // sso-session, services and other non-profile sections
		}
	}
	if p, ok := byName[name]; ok {
		return p
	}
	p := &Profile{Name: name}
	byName[name] = p
	return p
}

// MatchProfiles returns the profiles matching any of the globs, in name order. A glob that
// matches nothing is an error, because a typo would otherwise skip accounts silently.
func MatchProfiles(globs []string, profiles []Profile) ([]Profile, error) {
	var out []Profile
	seen := map[string]bool{}
	for _, g := range globs {
		if _, err := path.Match(g, ""); err != nil {
			return nil, fmt.Errorf("profile glob %q: %w", g, err)
		}
		n := 0
		for _, p := range profiles {
			if ok, _ := path.Match(g, p.Name); ok {
				n++
				if !seen[p.Name] {
					seen[p.Name] = true
					out = append(out, p)
				}
			}
		}
		if n == 0 {
			return nil, fmt.Errorf("%w: no profile matches %q", ErrNoProfile, g)
		}
	}
	slices.SortFunc(out, func(a, b Profile) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}
