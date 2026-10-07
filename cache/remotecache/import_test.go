package remotecache

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	cerrdefs "github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

type readerAtProvider struct{}

func (readerAtProvider) ReaderAt(context.Context, ocispecs.Descriptor) (content.ReaderAt, error) {
	return nil, errors.New("not implemented")
}

// infoStore is a cache store that can tell whether a blob exists, like the
// local cache's session store, and counts how often it is asked.
type infoStore struct {
	readerAtProvider
	blobs map[digest.Digest]bool
	calls map[digest.Digest]int
}

func (s *infoStore) Info(_ context.Context, dgst digest.Digest) (content.Info, error) {
	s.calls[dgst]++
	if !s.blobs[dgst] {
		return content.Info{}, errors.Wrapf(cerrdefs.ErrNotFound, "blob %s", dgst)
	}
	return content.Info{Digest: dgst}, nil
}

func TestImportedLayerInfo(t *testing.T) {
	ctx := t.Context()
	present := ocispecs.Descriptor{Digest: digest.FromString("present")}
	missing := ocispecs.Descriptor{Digest: digest.FromString("missing")}

	store := &infoStore{
		blobs: map[digest.Digest]bool{present.Digest: true},
		calls: map[digest.Digest]int{},
	}
	ci := NewImporter(store).(*contentCacheImporter)
	for range 2 {
		_, err := ci.layerProvider(present).Info(ctx, present.Digest)
		require.NoError(t, err)
		_, err = ci.layerProvider(missing).Info(ctx, missing.Digest)
		require.ErrorIs(t, err, cerrdefs.ErrNotFound)
	}
	require.Equal(t, map[digest.Digest]int{present.Digest: 1, missing.Digest: 1}, store.calls, "the store is asked once per blob")

	// A registry cache's provider can't tell: layers answer from their
	// descriptor, as before.
	ci = NewImporter(readerAtProvider{}).(*contentCacheImporter)
	_, err := ci.layerProvider(missing).Info(ctx, missing.Digest)
	require.NoError(t, err)
}
