package solver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestPrepareCacheExport(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  NewInMemoryCacheManager(),
	})
	defer l.Close()

	j, err := l.NewJob("j0")
	require.NoError(t, err)
	defer func() {
		if j != nil {
			j.Discard()
		}
	}()

	branch := func(v int) Edge {
		return Edge{Vertex: vtxSum(v, vtxOpt{inputs: []Edge{{Vertex: vtxConst(v, vtxOpt{})}}})}
	}
	res, err := j.Build(ctx, Edge{Vertex: vtxSum(1, vtxOpt{
		inputs: []Edge{branch(10), branch(20), branch(30)},
	})})
	require.NoError(t, err)
	require.NoError(t, j.Discard())
	j = nil
	exporter := res.CacheKeys()[0].Exporter

	resolvedBy := func(opt CacheExportOpt, wrap func(Result) error) (CacheExportOpt, func() map[string]int) {
		var mu sync.Mutex
		resolved := map[string]int{}
		resolve := opt.ResolveRemotes
		opt.ResolveRemotes = func(ctx context.Context, r Result) ([]*Remote, error) {
			if err := wrap(r); err != nil {
				return nil, err
			}
			mu.Lock()
			resolved[r.ID()]++
			mu.Unlock()
			return resolve(ctx, r)
		}
		return opt, func() map[string]int {
			mu.Lock()
			defer mu.Unlock()
			return resolved
		}
	}

	exportOpt, exported := resolvedBy(testExporterOpts(true), func(Result) error { return nil })
	_, err = exporter.ExportTo(ctx, newTestExporterTarget(), exportOpt)
	require.NoError(t, err)
	require.Len(t, exported(), 4, "the root and the three branch results")

	// The three branches are independent: each resolution waits for the other
	// two, which only completes if they run concurrently.
	var arrivals sync.WaitGroup
	arrivals.Add(3)
	all := make(chan struct{})
	go func() {
		arrivals.Wait()
		close(all)
	}()
	var once sync.Map
	prepareOpt, prepared := resolvedBy(testExporterOpts(true), func(r Result) error {
		if r.ID() == res.ID() {
			return nil
		}
		if _, loaded := once.LoadOrStore(r.ID(), struct{}{}); !loaded {
			arrivals.Done()
		}
		select {
		case <-all:
			return nil
		case <-time.After(10 * time.Second):
			return errors.New("branch results were not prepared concurrently")
		}
	})
	require.NoError(t, PrepareCacheExport(ctx, exporter, prepareOpt, 4))
	require.Equal(t, exported(), prepared(), "prepares exactly the results ExportTo resolves, once each")
}
