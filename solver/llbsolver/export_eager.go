package llbsolver

import (
	"context"
	"runtime"
	"sync"

	cacheconfig "github.com/moby/buildkit/cache/config"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/compression"
	"github.com/moby/buildkit/worker"
	"github.com/pkg/errors"
	"golang.org/x/sync/semaphore"
)

// eagerCacheExport creates the blobs of a mode=max cache export while the
// build runs. Each result is compressed as soon as its vertex completes, so
// the export finds the blobs of new layers ready instead of creating them one
// after the other once the build is done.
type eagerCacheExport struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	refCfg cacheconfig.RefConfig
	group  session.Group
	sem    *semaphore.Weighted
	wg     sync.WaitGroup

	mu   sync.Mutex
	seen map[string]struct{}
}

// newEagerCacheExport returns nil when the blobs of exp cannot be created
// early: blobs are created once per result, in the compression of whichever
// export asks first, and later exports reuse them unless they force a
// compression. Creating them early must not change what the other exports
// produce.
func newEagerCacheExport(ctx context.Context, exp ExporterRequest, g session.Group) *eagerCacheExport {
	comp, ok := eagerCacheExportCompression(exp)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithCancelCause(ctx)
	return &eagerCacheExport{
		ctx:    ctx,
		cancel: cancel,
		refCfg: cacheconfig.RefConfig{Compression: comp},
		group:  g,
		// Compression competes with the build for CPU.
		sem:  semaphore.NewWeighted(int64(max(1, runtime.GOMAXPROCS(0)/4))),
		seen: map[string]struct{}{},
	}
}

func eagerCacheExportCompression(exp ExporterRequest) (compression.Config, bool) {
	// Exporters found through the session are only known after the build.
	if exp.EnableSessionExporter {
		return compression.Config{}, false
	}
	var comp *compression.Config
	maxMode := false
	for _, ce := range exp.CacheExporters {
		c := ce.Config().Compression
		if comp != nil && !sameCompression(*comp, c) {
			return compression.Config{}, false
		}
		comp = &c
		maxMode = maxMode || ce.CacheExportMode == solver.CacheExportModeMax
	}
	if !maxMode {
		return compression.Config{}, false
	}
	for _, e := range exp.Exporters {
		switch e.Type() {
		case client.ExporterLocal, client.ExporterTar:
			continue
		}
		if c := e.Config().Compression(); !c.Force && !sameCompression(*comp, c) {
			return compression.Config{}, false
		}
	}
	return *comp, true
}

func sameCompression(a, b compression.Config) bool {
	if a.Type != b.Type {
		return false
	}
	if a.Level == nil || b.Level == nil {
		return a.Level == b.Level
	}
	return *a.Level == *b.Level
}

func (e *eagerCacheExport) hook(_ context.Context, results []solver.Result) {
	for _, res := range results {
		workerRef, ok := res.Sys().(*worker.WorkerRef)
		if !ok || workerRef.ImmutableRef == nil {
			continue
		}
		e.mu.Lock()
		_, seen := e.seen[workerRef.ImmutableRef.ID()]
		e.seen[workerRef.ImmutableRef.ID()] = struct{}{}
		e.mu.Unlock()
		if seen {
			continue
		}
		ref := workerRef.ImmutableRef.Clone()
		e.wg.Go(func() {
			defer ref.Release(context.WithoutCancel(e.ctx))
			if err := e.sem.Acquire(e.ctx, 1); err != nil {
				return
			}
			defer e.sem.Release(1)
			// The export resolves the same remotes and reports errors.
			if _, err := ref.GetRemotes(e.ctx, true, e.refCfg, false, e.group); err != nil {
				bklog.G(e.ctx).WithError(err).Debug("failed to prepare cache blob during build")
			}
		})
	}
}

func (e *eagerCacheExport) close() {
	e.cancel(errors.WithStack(context.Canceled))
	e.wg.Wait()
}
