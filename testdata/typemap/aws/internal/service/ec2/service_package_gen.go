package ec2

func (p *servicePackage) SDKResources(ctx context.Context) []*inttypes.ServicePackageSDKResource {
	return []*inttypes.ServicePackageSDKResource{
		{
			Factory:  resourceVPC,
			TypeName: "aws_vpc",
			Name:     "VPC",
			Region:   inttypes.ResourceRegionDefault(),
			Identity: inttypes.RegionalSingleParameterIdentity(inttypes.StringIdentityAttribute(names.AttrID, true)),
		},
		{
			Factory:  resourceSecurityGroupRule,
			TypeName: "aws_security_group_rule",
			Name:     "Security Group Rule",
			Region:   inttypes.ResourceRegionDefault(),
		},
	}
}

func (p *servicePackage) SDKDataSources(ctx context.Context) []*inttypes.ServicePackageSDKDataSource {
	return []*inttypes.ServicePackageSDKDataSource{
		{
			Factory:  dataSourceVPC,
			TypeName: "aws_vpc",
			Name:     "VPC",
		},
	}
}
