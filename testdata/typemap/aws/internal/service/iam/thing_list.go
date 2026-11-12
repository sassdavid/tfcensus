package iam

// @FrameworkListResource("aws_iam_thing")
func newThingResourceAsListResource() list.ListResourceWithConfigure {
	l := thingListResource{}
	return &l
}

type thingListResource struct {
	framework.WithList
}

func (l *thingListResource) ListResourceConfigSchema(_ context.Context, _ list.ListResourceSchemaRequest, response *list.ListResourceSchemaResponse) {
	response.Schema = listschema.Schema{
		Attributes: map[string]listschema.Attribute{
			"zone_id": listschema.StringAttribute{
				Required: true,
			},
			names.AttrName: listschema.StringAttribute{
				Required:    true,
				Description: "Name.",
			},
		},
		Blocks: map[string]listschema.Block{
			"filter": listschema.ListNestedBlock{
				NestedObject: listschema.NestedBlockObject{
					Attributes: map[string]listschema.Attribute{
						"values": listschema.ListAttribute{Required: true},
					},
				},
			},
		},
	}
}
