package iam

// @FrameworkListResource("aws_iam_policy")
func newPolicyResourceAsListResource() list.ListResourceWithConfigure {
	return &policyListResource{}
}

type policyListResource struct {
	framework.WithList
}

func (l *policyListResource) ListResourceConfigSchema(_ context.Context, _ list.ListResourceSchemaRequest, response *list.ListResourceSchemaResponse) {
	response.Schema = listschema.Schema{
		Attributes: map[string]listschema.Attribute{
			"path_prefix": listschema.StringAttribute{
				Optional: true,
			},
		},
	}
}
