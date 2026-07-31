package cacheimport

import (
	"context"
	"testing"

	"github.com/moby/buildkit/solver"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestMarshalRemoteStripsSourceReference(t *testing.T) {
	dgst := digest.FromString("layer")
	remote := &solver.Remote{Descriptors: []ocispecs.Descriptor{{
		Digest: dgst,
		Annotations: map[string]string{
			"containerd.io/distribution.source.ref": "example.invalid/source:cache",
			"containerd.io/uncompressed":            digest.FromString("diff").String(),
		},
	}}}
	state := &marshalState{
		chainsByID:  map[string]int{},
		descriptors: DescriptorProvider{},
	}

	marshalRemote(context.Background(), remote, state)

	desc := state.descriptors[dgst].Descriptor
	if _, ok := desc.Annotations["containerd.io/distribution.source.ref"]; ok {
		t.Fatal("source reference annotation was exported")
	}
	if desc.Annotations["containerd.io/uncompressed"] == "" {
		t.Fatal("uncompressed digest annotation was removed")
	}
	if remote.Descriptors[0].Annotations["containerd.io/distribution.source.ref"] == "" {
		t.Fatal("input descriptor was mutated")
	}
}
