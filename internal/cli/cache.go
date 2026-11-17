package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/probe"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/scan"
)

func newCacheCmd(d *app.Deps, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Inspect or clear the probe and scan caches",
		Long: `tfcensus keeps two caches under $XDG_CACHE_HOME/tfcensus:

  probe  doctor's and scan's capability probe, one file per account and region, fresh for 24h
  scan   scan's inventory and state index, one file per set of inputs, fresh for cache.ttl (1h)

They hold identifiers, tags and probe results, never state attribute values. doctor --refresh
and scan --refresh ignore them for one run.`,
		Example: `  tfcensus cache info
  tfcensus cache clear`,
		Args: noArgs,
	}
	cmd.AddCommand(newCacheInfoCmd(d, g), newCacheClearCmd(d, g))
	return cmd
}

// cacheKind is one of the caches under the cache directory, with how to tell a fresh file.
type cacheKind struct {
	name  string
	ttl   time.Duration
	fresh func(path string) bool
}

type cacheReport struct {
	Dir    string       `json:"dir"`
	Caches []cacheStats `json:"caches"`
}

type cacheStats struct {
	Name    string `json:"name"`
	Dir     string `json:"dir"`
	TTL     string `json:"ttl,omitempty"`
	Entries int    `json:"entries"`
	Bytes   int64  `json:"bytes"`
	Fresh   *int   `json:"fresh,omitempty"`
	Stale   *int   `json:"stale,omitempty"`
}

func newCacheInfoCmd(d *app.Deps, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show each cache's directory, entry count, size and freshness",
		Long: `info walks both caches and prints, for each one, its directory, the number of entries, their
size in bytes, how many are still fresh and how many are stale, and the time to live. It reads
only file names and times, never the cached data.`,
		Example: `  tfcensus cache info
  tfcensus cache info --format json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, kinds, err := g.caches(cmd, d)
			if err != nil {
				return err
			}
			rep := cacheReport{Dir: dir, Caches: make([]cacheStats, 0, len(kinds))}
			for _, k := range kinds {
				s, statErr := walkCache(dir, k, true)
				if statErr != nil {
					return statErr
				}
				rep.Caches = append(rep.Caches, s)
			}
			return writeCacheReport(d, g, rep, []string{"CACHE", "ENTRIES", "BYTES", "FRESH", "STALE", "TTL"},
				func(s cacheStats) []string {
					return []string{
						s.Name, strconv.Itoa(s.Entries), strconv.FormatInt(s.Bytes, 10),
						strconv.Itoa(*s.Fresh), strconv.Itoa(*s.Stale), s.TTL,
					}
				})
		},
	}
	setFormats(cmd, textFormats()...)
	setExitCodes(cmd)
	return cmd
}

func newCacheClearCmd(d *app.Deps, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Delete every entry of both caches",
		Long: `clear deletes every file in the probe and scan caches and prints how many entries and bytes it
removed. The next doctor or scan probes and collects again.`,
		Example: `  tfcensus cache clear
  tfcensus cache clear --format json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, kinds, err := g.caches(cmd, d)
			if err != nil {
				return err
			}
			rep := cacheReport{Dir: dir, Caches: make([]cacheStats, 0, len(kinds))}
			for _, k := range kinds {
				s, statErr := walkCache(dir, k, false)
				if statErr != nil {
					return statErr
				}
				if err = os.RemoveAll(s.Dir); err != nil {
					return fmt.Errorf("clear %s cache: %w", k.name, err)
				}
				s.TTL = ""
				rep.Caches = append(rep.Caches, s)
			}
			return writeCacheReport(d, g, rep, []string{"CACHE", "REMOVED", "BYTES"},
				func(s cacheStats) []string {
					return []string{s.Name, strconv.Itoa(s.Entries), strconv.FormatInt(s.Bytes, 10)}
				})
		},
	}
	setFormats(cmd, textFormats()...)
	setExitCodes(cmd)
	return cmd
}

// caches loads the config and returns the cache directory and the caches in it.
func (g *globals) caches(cmd *cobra.Command, d *app.Deps) (string, []cacheKind, error) {
	res, err := g.loadConfig(cmd, d)
	if err != nil {
		return "", nil, err
	}
	sc, err := newScanCache(d, &res.Config)
	if err != nil {
		return "", nil, err
	}
	now := d.Clock()
	return sc.Dir, []cacheKind{
		{
			name:  "probe",
			ttl:   probe.DefaultTTL,
			fresh: func(p string) bool { return probe.Fresh(p, probe.DefaultTTL, now) },
		},
		{name: "scan", ttl: sc.TTL, fresh: sc.Fresh},
	}, nil
}

// walkCache counts the files of one cache. With fresh set it also reads each file to count the
// fresh ones; a temporary or unreadable file is stale.
func walkCache(dir string, k cacheKind, fresh bool) (cacheStats, error) {
	s := cacheStats{Name: k.name, Dir: filepath.Join(dir, k.name), TTL: formatTTL(k.ttl)}
	var nFresh int
	err := filepath.WalkDir(s.Dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.Type().IsRegular() {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err //nolint:wrapcheck // wrapped once below with the cache name
		}
		s.Entries++
		s.Bytes += info.Size()
		if fresh && k.fresh(path) {
			nFresh++
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return s, fmt.Errorf("read %s cache: %w", k.name, err)
	}
	if fresh {
		stale := s.Entries - nFresh
		s.Fresh, s.Stale = &nFresh, &stale
	}
	return s, nil
}

// formatTTL drops the zero minutes and seconds Go prints, so 24h0m0s reads 24h.
func formatTTL(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

func writeCacheReport(
	d *app.Deps,
	g *globals,
	rep cacheReport,
	cols []string,
	row func(cacheStats) []string,
) error {
	if g.outFormat == render.FormatJSON {
		return render.JSON(d.Stdout, render.NewEnvelope(buildinfo.Get().Version, d.Clock(), rep))
	}
	tw := tabwriter.NewWriter(d.Stdout, 0, 0, 2, ' ', 0) //nolint:mnd // two spaces between columns
	_, _ = fmt.Fprintf(tw, "dir\t%s\n\n", rep.Dir)
	_, _ = fmt.Fprintln(tw, strings.Join(cols, "\t"))
	for _, s := range rep.Caches {
		_, _ = fmt.Fprintln(tw, strings.Join(row(s), "\t"))
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

// newScanCache returns the scan snapshot cache under $XDG_CACHE_HOME/tfcensus with cache.ttl.
func newScanCache(d *app.Deps, cfg *config.Config) (*scan.Cache, error) {
	dir, err := config.CacheDirPath(d.Env.Lookup, runtime.GOOS)
	if err != nil {
		return nil, fmt.Errorf("find cache directory: %w", err)
	}
	ttl, err := time.ParseDuration(cmp.Or(cfg.Cache.TTL, "1h"))
	if err != nil {
		return nil, usageError(fmt.Errorf("cache.ttl %q: %w", cfg.Cache.TTL, err))
	}
	return &scan.Cache{Dir: dir, TTL: ttl, Now: d.Clock}, nil
}
