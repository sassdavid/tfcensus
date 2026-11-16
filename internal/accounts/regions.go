package accounts

import (
	"context"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/account"
	"github.com/aws/aws-sdk-go-v2/service/account/types"
)

// EnabledRegions lists the regions acct can use: the ones enabled by default and the opt-in
// regions it turned on, sorted.
func EnabledRegions(ctx context.Context, acct *Account) ([]string, error) {
	client := account.NewFromConfig(acct.Config)
	pages := account.NewListRegionsPaginator(client, &account.ListRegionsInput{
		RegionOptStatusContains: []types.RegionOptStatus{
			types.RegionOptStatusEnabled, types.RegionOptStatusEnabledByDefault,
		},
	})
	var out []string
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			if IsAccessDenied(err) {
				return nil, fmt.Errorf("list enabled regions of %s: access denied for account:ListRegions", acct.ID)
			}
			return nil, fmt.Errorf("list enabled regions of %s: %w", acct.ID, err)
		}
		for _, r := range page.Regions {
			out = append(out, aws.ToString(r.RegionName))
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}
