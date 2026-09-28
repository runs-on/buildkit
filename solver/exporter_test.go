package solver

import (
	"slices"
	"testing"
	"time"

	"github.com/moby/buildkit/util/compression"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestRemoteMatchesCompression(t *testing.T) {
	gzipLayers := &Remote{Descriptors: []ocispecs.Descriptor{
		{MediaType: ocispecs.MediaTypeImageLayerGzip},
		{MediaType: ocispecs.MediaTypeImageLayerGzip},
	}}
	zstdLayers := &Remote{Descriptors: []ocispecs.Descriptor{
		{MediaType: ocispecs.MediaTypeImageLayerZstd},
	}}

	tests := []struct {
		name   string
		remote *Remote
		config compression.Config
		match  bool
	}{
		{name: "nil remote", config: compression.New(compression.Gzip)},
		{name: "empty remote", remote: &Remote{}, config: compression.New(compression.Gzip)},
		{name: "all gzip", remote: gzipLayers, config: compression.New(compression.Gzip), match: true},
		{
			name: "mixed compression",
			remote: &Remote{Descriptors: []ocispecs.Descriptor{
				{MediaType: ocispecs.MediaTypeImageLayerGzip},
				{MediaType: ocispecs.MediaTypeImageLayerZstd},
			}},
			config: compression.New(compression.Gzip),
		},
		{name: "zstd", remote: zstdLayers, config: compression.New(compression.Zstd), match: true},
		{name: "forced zstd", remote: zstdLayers, config: compression.New(compression.Zstd).SetForce(true), match: true},
		{name: "estargz without force", remote: gzipLayers, config: compression.New(compression.EStargz), match: true},
		{name: "forced gzip", remote: gzipLayers, config: compression.New(compression.Gzip).SetForce(true)},
		{name: "forced estargz", remote: gzipLayers, config: compression.New(compression.EStargz).SetForce(true)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.match, remoteMatchesCompression(tt.remote, tt.config))
		})
	}
}

func TestCompareCacheRecord(t *testing.T) {
	now := time.Now()
	a := &CacheRecord{CreatedAt: now, Priority: 1}
	b := &CacheRecord{CreatedAt: now, Priority: 2}
	c := &CacheRecord{CreatedAt: now.Add(1 * time.Second), Priority: 1}
	d := &CacheRecord{CreatedAt: now.Add(-1 * time.Second), Priority: 1}

	records := []*CacheRecord{b, nil, d, a, c, nil}
	slices.SortFunc(records, compareCacheRecord)

	names := map[*CacheRecord]string{
		a:   "a",
		b:   "b",
		c:   "c",
		d:   "d",
		nil: "nil",
	}
	var got []string
	for _, r := range records {
		got = append(got, names[r])
	}
	want := []string{"c", "a", "b", "d", "nil", "nil"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected order: got %v, want %v", got, want)
	}
}
