package llbsolver

import (
	"testing"

	"github.com/moby/buildkit/cache/remotecache"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/exporter"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/util/compression"
	"github.com/stretchr/testify/require"
)

type compressionCacheExporter struct {
	remotecache.Exporter
	comp compression.Config
}

func (e compressionCacheExporter) Config() remotecache.Config {
	return remotecache.Config{Compression: e.comp}
}

type compressionExporter struct {
	exporter.ExporterInstance
	typ  string
	comp compression.Config
}

func (e compressionExporter) Type() string { return e.typ }

func (e compressionExporter) Config() *exporter.Config {
	return exporter.NewConfigWithCompression(e.comp)
}

func TestEagerCacheExportCompression(t *testing.T) {
	zstd := compression.New(compression.Zstd).SetLevel(1)
	zstd3 := compression.New(compression.Zstd).SetLevel(3)
	gzip := compression.New(compression.Gzip)
	forcedGzip := compression.New(compression.Gzip).SetForce(true)
	cache := func(mode solver.CacheExportMode, comp compression.Config) RemoteCacheExporter {
		return RemoteCacheExporter{Exporter: compressionCacheExporter{comp: comp}, CacheExportMode: mode}
	}
	image := func(typ string, comp compression.Config) exporter.ExporterInstance {
		return compressionExporter{typ: typ, comp: comp}
	}

	for _, tc := range []struct {
		name string
		exp  ExporterRequest
		want bool
	}{
		{name: "no cache export", exp: ExporterRequest{}},
		{
			name: "min mode only",
			exp:  ExporterRequest{CacheExporters: []RemoteCacheExporter{cache(solver.CacheExportModeMin, zstd)}},
		},
		{
			name: "max mode without image",
			exp:  ExporterRequest{CacheExporters: []RemoteCacheExporter{cache(solver.CacheExportModeMax, zstd)}},
			want: true,
		},
		{
			name: "image with the same compression",
			exp: ExporterRequest{
				CacheExporters: []RemoteCacheExporter{cache(solver.CacheExportModeMax, zstd)},
				Exporters:      []exporter.ExporterInstance{image(client.ExporterImage, zstd)},
			},
			want: true,
		},
		{
			name: "image with another compression",
			exp: ExporterRequest{
				CacheExporters: []RemoteCacheExporter{cache(solver.CacheExportModeMax, zstd)},
				Exporters:      []exporter.ExporterInstance{image(client.ExporterImage, gzip)},
			},
		},
		{
			name: "image with another compression level",
			exp: ExporterRequest{
				CacheExporters: []RemoteCacheExporter{cache(solver.CacheExportModeMax, zstd)},
				Exporters:      []exporter.ExporterInstance{image(client.ExporterOCI, zstd3)},
			},
		},
		{
			name: "image forcing its compression",
			exp: ExporterRequest{
				CacheExporters: []RemoteCacheExporter{cache(solver.CacheExportModeMax, zstd)},
				Exporters:      []exporter.ExporterInstance{image(client.ExporterDocker, forcedGzip)},
			},
			want: true,
		},
		{
			name: "local export creates no blobs",
			exp: ExporterRequest{
				CacheExporters: []RemoteCacheExporter{cache(solver.CacheExportModeMax, zstd)},
				Exporters:      []exporter.ExporterInstance{image(client.ExporterLocal, gzip)},
			},
			want: true,
		},
		{
			name: "session exporters",
			exp: ExporterRequest{
				CacheExporters:        []RemoteCacheExporter{cache(solver.CacheExportModeMax, zstd)},
				EnableSessionExporter: true,
			},
		},
		{
			name: "cache exports with different compressions",
			exp: ExporterRequest{CacheExporters: []RemoteCacheExporter{
				cache(solver.CacheExportModeMax, zstd),
				cache(solver.CacheExportModeMin, gzip),
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp, ok := eagerCacheExportCompression(tc.exp)
			require.Equal(t, tc.want, ok)
			if ok {
				require.Equal(t, tc.exp.CacheExporters[0].Config().Compression, comp)
			}
		})
	}
}
