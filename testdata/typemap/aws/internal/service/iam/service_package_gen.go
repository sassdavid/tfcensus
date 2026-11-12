package iam

func (p *servicePackage) FrameworkResources(ctx context.Context) []*inttypes.ServicePackageFrameworkResource {
	return []*inttypes.ServicePackageFrameworkResource{
		{
			Factory:  newRecordResource,
			TypeName: "aws_iam_thing",
			Name:     "Thing",
			Region:   inttypes.ResourceRegionDisabled(),
			Identity: inttypes.GlobalParameterizedIdentity([]inttypes.IdentityAttribute{
				inttypes.StringIdentityAttribute("zone_id", true),
				inttypes.StringIdentityAttributeWithMappedName(names.AttrName, true, "fqdn"),
				inttypes.StringIdentityAttribute(names.AttrType, true),
				inttypes.StringIdentityAttribute("set_identifier", false),
			}),
		},
		{
			Factory:  newPolicyResource,
			TypeName: "aws_iam_policy",
			Name:     "Policy",
			Region:   inttypes.ResourceRegionDisabled(),
			Identity: inttypes.GlobalARNIdentity(),
		},
	}
}

func (p *servicePackage) FrameworkListResources(ctx context.Context) iter.Seq[*inttypes.ServicePackageFrameworkListResource] {
	return slices.Values([]*inttypes.ServicePackageFrameworkListResource{
		{
			Factory:  newPolicyResourceAsListResource,
			TypeName: "aws_iam_policy",
			Name:     "Policy",
			Region:   inttypes.ResourceRegionDisabled(),
			Identity: inttypes.GlobalARNIdentity(),
		},
		{
			Factory:  newThingResourceAsListResource,
			TypeName: "aws_iam_thing",
			Name:     "Thing",
			Region:   inttypes.ResourceRegionDisabled(),
		},
	})
}
