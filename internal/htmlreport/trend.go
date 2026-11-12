package htmlreport

import (
	"slices"
	"strconv"
	"strings"

	"github.com/sassdavid/tfcensus/internal/report"
	"github.com/sassdavid/tfcensus/internal/scan"
)

// Trend chart geometry, in SVG user units.
const (
	trendW      = 640
	trendH      = 220
	trendLeft   = 44
	trendRight  = 16
	trendTop    = 12
	trendBottom = 32
	percent     = 100
	totalSeries = "total"
)

type gridLine struct{ Y, Label string }

type xLabel struct{ X, Label string }

type dot struct{ X, Y, Title string }

type series struct {
	Name, Color, Points string
	Dots                []dot
}

// trend is the coverage of every scan over time: the total and, for up to maxSeries accounts,
// one line each. Points are evenly spaced in scan order.
type trend struct {
	Width, Height int
	Grid          []gridLine
	XLabels       []xLabel
	Series        []series
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) }

func buildTrend(scans []*report.Saved) *trend {
	plotW := float64(trendW - trendLeft - trendRight)
	plotH := float64(trendH - trendTop - trendBottom)
	t := &trend{Width: trendW, Height: trendH}
	for _, pct := range []float64{0, 25, 50, 75, 100} {
		y := float64(trendTop) + (percent-pct)/percent*plotH
		t.Grid = append(t.Grid, gridLine{Y: num(y), Label: strconv.Itoa(int(pct)) + "%"})
	}
	x := func(i int) float64 {
		if len(scans) == 1 {
			return float64(trendLeft) + plotW/2
		}
		return float64(trendLeft) + float64(i)*plotW/float64(len(scans)-1)
	}
	for i, s := range scans {
		t.XLabels = append(t.XLabels, xLabel{X: num(x(i)), Label: s.GeneratedAt.UTC().Format("2006-01-02")})
	}
	accounts := map[string]bool{}
	for _, s := range scans {
		for _, a := range s.Scan.Accounts {
			accounts[a.Account] = true
		}
	}
	names := []string{totalSeries}
	if len(accounts) > 1 && len(accounts) <= maxSeries {
		names = append(names, values(accounts)...)
	}
	colors := palette()
	for n, name := range names {
		se := series{Name: name, Color: colors[n%len(colors)]}
		var pts []string
		for i, s := range scans {
			pct, ok := scanCoverage(s.Scan, name)
			if !ok {
				continue
			}
			y := float64(trendTop) + (percent-pct)/percent*plotH
			pts = append(pts, num(x(i))+","+num(y))
			se.Dots = append(
				se.Dots,
				dot{X: num(x(i)), Y: num(y), Title: name + " " + num(pct) + "% on " + t.XLabels[i].Label},
			)
		}
		se.Points = strings.Join(pts, " ")
		t.Series = append(t.Series, se)
	}
	return t
}

// scanCoverage returns the coverage of one account, or of every account for "total".
func scanCoverage(rep *scan.Report, account string) (float64, bool) {
	total := make(scan.Counts)
	for _, a := range rep.Accounts {
		if account != totalSeries && a.Account != account {
			continue
		}
		for _, c := range scan.Categories() {
			total[c] += a.Counts[c]
		}
	}
	if account != totalSeries &&
		!slices.ContainsFunc(rep.Accounts, func(s scan.Summary) bool { return s.Account == account }) {
		return 0, false
	}
	return total.Coverage()
}
