package cacheimport

import (
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/util/compression"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// sessionProvider records the session group it was bound to.
type sessionProvider struct {
	countingProvider
	g session.Group
}

func (p *sessionProvider) ProviderForSession(g session.Group) content.Provider {
	return &sessionProvider{g: g}
}

func TestLoadRemotes(t *testing.T) {
	gzipLayers := []ocispecs.Descriptor{
		{Digest: dgst("g0"), MediaType: ocispecs.MediaTypeImageLayerGzip},
		{Digest: dgst("g1"), MediaType: ocispecs.MediaTypeImageLayerGzip},
	}
	zstdLayers := []ocispecs.Descriptor{
		{Digest: dgst("z0"), MediaType: ocispecs.MediaTypeImageLayerZstd},
	}

	tests := []struct {
		name    string
		layers  []ocispecs.Descriptor
		comp    *compression.Config
		matched bool
	}{
		{name: "no compression option", layers: gzipLayers, matched: true},
		{name: "gzip", layers: gzipLayers, comp: new(compression.New(compression.Gzip)), matched: true},
		{name: "zstd requested for gzip layers", layers: gzipLayers, comp: new(compression.New(compression.Zstd))},
		{name: "forced zstd for zstd layers", layers: zstdLayers, comp: new(compression.New(compression.Zstd).SetForce(true)), matched: true},
		{name: "forced zstd for gzip layers", layers: gzipLayers, comp: new(compression.New(compression.Zstd).SetForce(true))},
		// gzip and eStargz layers share a media type: only converting them can
		// tell the caller which one it has.
		{name: "forced gzip", layers: gzipLayers, comp: new(compression.New(compression.Gzip).SetForce(true))},
		{name: "forced estargz", layers: gzipLayers, comp: new(compression.New(compression.EStargz).SetForce(true))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := &solver.Remote{Descriptors: tt.layers, Provider: &sessionProvider{}}
			cc := NewCacheChains()
			_, ok, err := cc.Add(outputKey(dgst("foo"), 0), nil, []solver.CacheExportResult{{CreatedAt: time.Now(), Result: remote}})
			require.NoError(t, err)
			require.True(t, ok)
			_, results, err := NewCacheKeyStorage(cc, nil)
			require.NoError(t, err)

			g := session.NewGroup("export")
			remotes, err := results.LoadRemotes(t.Context(), solver.CacheResult{ID: remoteID(remote)}, tt.comp, g)
			require.NoError(t, err)
			if !tt.matched {
				require.Empty(t, remotes)
				return
			}
			require.Len(t, remotes, 1)
			require.Equal(t, tt.layers, remotes[0].Descriptors)
			require.Equal(t, g, remotes[0].Provider.(*sessionProvider).g, "blobs are read with the exporting session")
		})
	}
}
