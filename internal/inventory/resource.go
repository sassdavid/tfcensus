package inventory

import (
	"context"
	"errors"
	"iter"
	"slices"

	"github.com/aws/smithy-go"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/probe"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// Layer names, in the order their attributes win when layers report the same resource.
const (
	LayerConfig         = "config"
	LayerRE             = "resource-explorer"
	LayerSGRules        = "native:ec2-sg-rules"
	LayerIAMAttachments = "native:iam-attachments"
	LayerRoute53Records = "native:route53-records"
	LayerS3Subresources = "native:s3-subresources"
	LayerCC             = "cloudcontrol"
)

// LayerOrder returns every layer name in precedence order.
func LayerOrder() []string {
	return []string{
		LayerConfig, LayerRE, LayerSGRules, LayerIAMAttachments, LayerRoute53Records, LayerS3Subresources, LayerCC,
	}
}

// layerAliases are the short names --layers accepts.
func layerAliases() map[string]string {
	return map[string]string{"re": LayerRE, "cc": LayerCC}
}

func layerRank(name string) int {
	if i := slices.Index(LayerOrder(), name); i >= 0 {
		return i
	}
	return len(LayerOrder())
}

// CloudResource is one live resource. Global resources (IAM, Route 53, S3 bucket names) have no
// region; their account comes from the ARN or the lister.
type CloudResource struct {
	Account    string            `json:"account"`
	Region     string            `json:"region,omitempty"`
	CFNType    string            `json:"cfn_type,omitempty"`
	SourceType string            `json:"source_type,omitempty"` // the layer's own type name, when the CloudFormation type is unknown
	ARN        string            `json:"arn,omitempty"`
	ID         string            `json:"id,omitempty"` // the layer's resource ID, such as Config's resourceId
	Name       string            `json:"name,omitempty"`
	TFTypes    []string          `json:"tf_types,omitempty"` // candidate TF types from the typemap
	Tags       map[string]string `json:"tags,omitempty"`
	Attrs      map[string]string `json:"attrs,omitempty"` // identifiers a native lister read, such as role and policy_arn
	Parents    []Ref             `json:"parents,omitempty"`
	// Relationships are every resource the layer relates it to, its parents included.
	Relationships []Ref    `json:"relationships,omitempty"`
	Created       string   `json:"created,omitempty"` // RFC 3339, when the layer knows it
	Sources       []string `json:"sources"`           // the layers that reported it, in precedence order
}

func (r *CloudResource) setAttr(k, v string) {
	if r.Attrs == nil {
		r.Attrs = map[string]string{}
	}
	r.Attrs[k] = v
}

// Ref points at another resource by ARN, or by type and ID when it has no ARN.
type Ref struct {
	CFNType      string `json:"cfn_type"`
	ID           string `json:"id,omitempty"`
	ARN          string `json:"arn,omitempty"`
	Relationship string `json:"relationship,omitempty"` // as the layer names it, such as "Is attached to Instance"
}

// Capabilities says which inventory sources work in one account and region.
type Capabilities struct {
	Config           bool // a recorder is recording
	ConfigGlobal     bool // and it records global types (IAM)
	ResourceExplorer bool // an index and a default view exist
}

// CapabilitiesFrom reads the capabilities from a probe result.
func CapabilitiesFrom(r *probe.Region) Capabilities {
	c := Capabilities{
		Config:           r.Config.Status == probe.StatusAvailable,
		ResourceExplorer: r.ResourceExplorer.Status == probe.StatusAvailable && r.ResourceExplorer.DefaultView,
	}
	for _, rec := range r.Config.Recorders {
		if rec.Recording && (rec.IncludeGlobal || rec.AllSupported) {
			c.ConfigGlobal = c.Config
		}
	}
	return c
}

// Layer lists live resources from one source.
type Layer interface {
	Name() string
	// Global reports whether the layer lists account-wide resources once per account, with an
	// empty region, instead of once per region.
	Global() bool
	Supports(c Capabilities) bool
	// Types returns the CloudFormation types the layer can return.
	Types() []string
	// List yields the resources of the given types (all of [Layer.Types] when empty). An error
	// ends the listing unless it is about one resource, such as one bucket.
	List(ctx context.Context, acct *accounts.Account, region string, types []string) iter.Seq2[CloudResource, error]
}

// GlobalRegion returns the region that serves a partition's global APIs (IAM, Route 53).
func GlobalRegion(partition string) string {
	switch partition {
	case "aws-cn":
		return "cn-north-1"
	case "aws-us-gov":
		return "us-gov-west-1"
	}
	return "us-east-1"
}

// Denied reports whether err is an authorization failure rather than an outage.
func Denied(err error) bool {
	apiErr, ok := errors.AsType[smithy.APIError](err)
	if !ok {
		return false
	}
	switch apiErr.ErrorCode() {
	case "AccessDenied", "AccessDeniedException", "UnauthorizedOperation", "UnauthorizedException",
		"AuthorizationError", "Forbidden":
		return true
	}
	return false
}

func apiCode(err error) string {
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		return apiErr.ErrorCode()
	}
	return ""
}

// wanted reports whether cfn is in types, or types is empty.
func wanted(types []string, cfn string) bool {
	return len(types) == 0 || slices.Contains(types, cfn)
}

// curatedTypes returns the CloudFormation types of the curated entries that keep returns true for.
func curatedTypes(m *typemap.Map, keep func(*typemap.Type) bool) []string {
	var out []string
	for _, t := range m.Curated() {
		if keep(t) {
			for _, c := range t.CFN {
				if !slices.Contains(out, c) {
					out = append(out, c)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}
