package solver

import (
	"context"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// PreparedCacheExport holds the results that PrepareCacheExport resolved, so
// ExportTo does not resolve them again.
type PreparedCacheExport struct {
	mu      sync.Mutex
	results map[*exporter]exportResult
}

func (p *PreparedCacheExport) lookup(e *exporter) (exportResult, bool) {
	if p == nil {
		return exportResult{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.results[e]
	return r, ok
}

func (p *PreparedCacheExport) store(e *exporter, r exportResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.results[e] = r
}

// PrepareCacheExport resolves the results that ExportTo exports, in parallel
// across independent exporters, and returns them for ExportTo to reuse through
// CacheExportOpt.Prepared. ExportTo walks exporters one at a time, so on a
// mode=max export it creates the blobs of independent stages sequentially.
// Like ExportTo, an exporter is prepared before the exporters it links to,
// which then usually find their blobs among the ones created for its chain.
// Failures are not reported: ExportTo resolves those exporters itself and
// reports the error, so the exported cache is the same.
func PrepareCacheExport(ctx context.Context, e CacheExporter, opt CacheExportOpt, parallelism int) (*PreparedCacheExport, error) {
	eg, egCtx := errgroup.WithContext(ctx)
	p := &exportPreparer{
		eg:       eg,
		ctx:      egCtx,
		opt:      opt,
		sem:      semaphore.NewWeighted(int64(max(1, parallelism))),
		visited:  map[CacheExporter]struct{}{},
		prepared: &PreparedCacheExport{results: map[*exporter]exportResult{}},
	}
	p.visit(ctx, e)
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return p.prepared, nil
}

type exportPreparer struct {
	eg       *errgroup.Group
	ctx      context.Context
	opt      CacheExportOpt
	sem      *semaphore.Weighted
	mu       sync.Mutex
	visited  map[CacheExporter]struct{}
	prepared *PreparedCacheExport
}

// visit prepares ce once, then the exporters it links to, with the contexts
// ExportTo passes them.
func (p *exportPreparer) visit(ctx context.Context, ce CacheExporter) {
	p.mu.Lock()
	_, seen := p.visited[ce]
	p.visited[ce] = struct{}{}
	p.mu.Unlock()
	if seen {
		return
	}

	p.eg.Go(func() error {
		switch e := ce.(type) {
		case *mergedExporter:
			for _, e := range e.exporters {
				p.visit(ctx, e)
			}
		case *exporter:
			mainCtx := ctx
			ctx := e.recordContext(ctx)
			if e.exportsRecord(p.opt) {
				if err := p.sem.Acquire(p.ctx, 1); err != nil {
					return err
				}
				r, err := e.exportResults(ctx, e.k, p.opt)
				p.sem.Release(1)
				if err == nil {
					p.prepared.store(e, r)
				}
			}
			e.eachChild(ctx, mainCtx, func(ctx context.Context, _ int, _ string, child CacheExporter) {
				p.visit(ctx, child)
			})
		}
		return nil
	})
}
