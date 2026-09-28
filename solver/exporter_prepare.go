package solver

import (
	"context"
	"slices"

	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// PrepareCacheExport resolves, in parallel, the remotes that ExportTo will need
// for the records it exports. ExportTo walks records one at a time, so on a
// mode=max export it creates the blobs of independent stages sequentially;
// preparing them first lets their diffing and compression use every CPU.
// Errors are left for ExportTo to report, so the exported cache is the same.
func PrepareCacheExport(ctx context.Context, e CacheExporter, opt CacheExportOpt, parallelism int) error {
	if parallelism < 1 {
		parallelism = 1
	}
	eg, egCtx := errgroup.WithContext(ctx)
	p := &exportPreparer{
		eg:      eg,
		ctx:     egCtx,
		opt:     opt,
		sem:     semaphore.NewWeighted(int64(parallelism)),
		visited: map[CacheExporter]struct{}{},
	}
	p.walk(ctx, e)
	return eg.Wait()
}

type exportPreparer struct {
	eg      *errgroup.Group
	ctx     context.Context
	opt     CacheExportOpt
	sem     *semaphore.Weighted
	visited map[CacheExporter]struct{}
}

// walk mirrors the traversal of ExportTo, including how record context options
// are inherited by dependencies but not by secondary exporters.
func (p *exportPreparer) walk(ctx context.Context, ce CacheExporter) {
	if _, ok := p.visited[ce]; ok {
		return
	}
	p.visited[ce] = struct{}{}

	switch e := ce.(type) {
	case *mergedExporter:
		for _, e := range e.exporters {
			p.walk(ctx, e)
		}
	case *exporter:
		mainCtx := ctx
		if CacheOptGetterOf(ctx) == nil && e.recordCtxOpts != nil {
			ctx = e.recordCtxOpts(ctx)
		}
		p.prepareRecord(ctx, e)
		for _, deps := range e.k.Deps() {
			for _, dep := range deps {
				p.walk(ctx, dep.CacheKey.Exporter)
			}
		}
		if e.edge != nil {
			for _, de := range e.edge.secondaryExporters {
				p.walk(mainCtx, de.cacheKey.CacheKey.Exporter)
			}
		}
	}
}

// prepareRecord selects the record ExportTo would export for e and, when its
// remote has to come from the local result, resolves that result in the
// background.
func (p *exportPreparer) prepareRecord(ctx context.Context, e *exporter) {
	if e.override != nil && !*e.override {
		return
	}
	if len(e.k.Deps()) == 0 && !p.opt.ExportRoots {
		return
	}
	if p.opt.Mode == CacheExportModeRemoteOnly {
		return
	}

	records := slices.Clone(e.records)
	slices.SortStableFunc(records, compareCacheRecord)
	if e.record != nil {
		records = append([]*CacheRecord{e.record}, records...)
	}
	for _, v := range records {
		cm := v.cacheManager
		res, err := cm.backend.Load(cm.getID(v.key), v.ID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return
		}
		remotes, err := cm.results.LoadRemotes(ctx, res, p.opt.CompressionOpt, p.opt.Session)
		if err != nil {
			return
		}
		if len(remotes) > 0 && (p.opt.CompressionOpt == nil || remoteMatchesCompression(remotes[0], *p.opt.CompressionOpt)) {
			return
		}
		p.eg.Go(func() error {
			if err := p.sem.Acquire(p.ctx, 1); err != nil {
				return err
			}
			defer p.sem.Release(1)
			r, err := cm.results.Load(ctx, res)
			if err != nil {
				return nil
			}
			defer r.Release(context.TODO())
			_, _ = p.opt.ResolveRemotes(ctx, r)
			return nil
		})
		return
	}
}
