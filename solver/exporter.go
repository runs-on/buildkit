package solver

import (
	"context"
	"errors"
	"slices"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/buildkit/util/compression"
	digest "github.com/opencontainers/go-digest"
)

type exporter struct {
	k             *CacheKey
	records       []*CacheRecord
	record        *CacheRecord
	recordCtxOpts func(context.Context) context.Context

	edge     *edge // for secondaryExporters
	override *bool
}

func addBacklinks(t CacheExporterTarget, cm *cacheManager, id string, bkm map[string][]CacheExporterRecord) ([]CacheExporterRecord, error) {
	out, ok := bkm[id]
	if ok && out != nil {
		return out, nil
	} else if ok && out == nil {
		return nil, nil
	}
	bkm[id] = nil

	m := map[digest.Digest][][]CacheLink{}
	isRoot := true
	if err := cm.backend.WalkBacklinks(id, func(id string, link CacheInfoLink) error {
		isRoot = false
		recs, err := addBacklinks(t, cm, id, bkm)
		if err != nil { // TODO: should we continue on error?
			return err
		}
		links := m[link.Digest]
		for int(link.Input) >= len(links) {
			links = append(links, nil)
		}
		for _, rec := range recs {
			links[int(link.Input)] = append(links[int(link.Input)], CacheLink{Src: rec, Selector: link.Selector.String()})
		}
		m[link.Digest] = links
		return nil
	}); err != nil {
		return nil, err
	}

	if isRoot {
		dgst, err := digest.Parse(id)
		if err == nil {
			rec, ok, err := t.Add(dgst, nil, nil)
			if err != nil {
				return nil, err
			}
			if ok && rec != nil {
				out = append(out, rec)
			}
		}
	}

	// validate that all inputs are present
	for dgst, links := range m {
		for _, links := range links {
			if len(links) == 0 {
				out = nil
				m[dgst] = nil
				break
			}
		}
	}

	for dgst, links := range m {
		if len(links) == 0 {
			continue
		}
		rec, ok, err := t.Add(dgst, links, nil)
		if err != nil {
			return nil, err
		}
		if !ok || rec == nil {
			continue
		}
		out = append(out, rec)
	}

	bkm[id] = out
	return out, nil
}

type contextT string

var (
	backlinkKey = contextT("solver/exporter/backlinks")
	resKey      = contextT("solver/exporter/res")
)

func (e *exporter) ExportTo(ctx context.Context, t CacheExporterTarget, opt CacheExportOpt) ([]CacheExporterRecord, error) {
	var bkm map[string][]CacheExporterRecord

	if bk := ctx.Value(backlinkKey); bk == nil {
		bkm = map[string][]CacheExporterRecord{}
		ctx = context.WithValue(ctx, backlinkKey, bkm)
	} else {
		bkm = bk.(map[string][]CacheExporterRecord)
	}

	var res map[*exporter][]CacheExporterRecord
	if r := ctx.Value(resKey); r == nil {
		res = map[*exporter][]CacheExporterRecord{}
		ctx = context.WithValue(ctx, resKey, res)
	} else {
		res = r.(map[*exporter][]CacheExporterRecord)
	}
	if v, ok := res[e]; ok {
		return v, nil
	}
	res[e] = nil

	deps := e.k.Deps()

	k := e.k.clone() // protect against *CacheKey internal ids mutation from other exports

	recKey := rootKey(k.Digest(), k.Output())

	mainCtx := ctx
	ctx = e.recordContext(ctx)

	v := e.record
	var results []CacheExportResult
	var remote *Remote
	if e.exportsRecord(opt) {
		r, ok := opt.Prepared.lookup(e)
		if !ok {
			var err error
			if r, err = e.exportResults(ctx, k, opt); err != nil {
				return nil, err
			}
		}
		v, results, remote = r.record, r.results, r.remote
	}

	if remote != nil && opt.Mode == CacheExportModeMin {
		opt.Mode = CacheExportModeRemoteOnly
	}

	srcs := make([][]CacheLink, len(deps))
	e.eachChild(ctx, mainCtx, func(ctx context.Context, index int, selector string, child CacheExporter) {
		recs, err := child.ExportTo(ctx, t, opt)
		if err != nil {
			return
		}
		for _, r := range recs {
			srcs[index] = append(srcs[index], CacheLink{Src: r, Selector: selector})
		}
	})

	if !opt.IgnoreBacklinks {
		for cm, id := range k.ids {
			_, err := addBacklinks(t, cm, id, bkm)
			if err != nil {
				return nil, err
			}
		}
	}

	// validate deps are present
	for _, deps := range srcs {
		if len(deps) == 0 {
			res[e] = nil
			return res[e], nil
		}
	}

	if v != nil && len(deps) == 0 {
		cm := v.cacheManager
		key := cm.getID(v.key)
		if err := cm.backend.WalkIDsByResult(v.ID, func(id string) error {
			if id == key {
				return nil
			}
			hasBacklinks := false
			cm.backend.WalkBacklinks(id, func(id string, link CacheInfoLink) error {
				hasBacklinks = true
				return nil
			})
			if hasBacklinks {
				return nil
			}

			dgst, err := digest.Parse(id)
			if err != nil {
				return nil
			}
			_, _, err = t.Add(dgst, nil, results)
			return err
		}); err != nil {
			return nil, err
		}
	}

	out, ok, err := t.Add(recKey, srcs, results)
	if err != nil {
		return nil, err
	}
	res[e] = []CacheExporterRecord{}
	if ok {
		res[e] = append(res[e], out)
	}
	return res[e], nil
}

// recordContext returns the context the record of e is resolved with, and
// that its dependencies are exported with.
func (e *exporter) recordContext(ctx context.Context) context.Context {
	if CacheOptGetterOf(ctx) == nil && e.recordCtxOpts != nil {
		return e.recordCtxOpts(ctx)
	}
	return ctx
}

// exportsRecord reports whether ExportTo exports the results of e's own
// record, as opposed to only linking it to its dependencies.
func (e *exporter) exportsRecord(opt CacheExportOpt) bool {
	if e.override != nil && !*e.override {
		return false
	}
	return opt.ExportRoots || len(e.k.Deps()) > 0
}

// eachChild calls fn for each exporter that ExportTo exports after e, in
// order, with the context ExportTo passes it and the index and selector of
// the dependency it links to.
func (e *exporter) eachChild(ctx, mainCtx context.Context, fn func(ctx context.Context, index int, selector string, child CacheExporter)) {
	for i, deps := range e.k.Deps() {
		for _, dep := range deps {
			fn(ctx, i, string(dep.Selector), dep.CacheKey.Exporter)
		}
	}
	if e.edge != nil {
		for _, de := range e.edge.secondaryExporters {
			fn(mainCtx, de.index, de.cacheKey.Selector.String(), de.cacheKey.CacheKey.Exporter)
		}
	}
}

// exportResult is what ExportTo exports for the record of an exporter.
type exportResult struct {
	// record is the record whose results are exported, nil if none of the
	// exporter's records still exists.
	record  *CacheRecord
	results []CacheExportResult
	remote  *Remote
}

// exportResults finds the first record of e that still exists and resolves
// the remotes of its result.
func (e *exporter) exportResults(ctx context.Context, k *CacheKey, opt CacheExportOpt) (exportResult, error) {
	records := slices.Clone(e.records)
	slices.SortStableFunc(records, compareCacheRecord)

	var r exportResult
	var i int
	v := e.record
	for {
		if v == nil {
			if i < len(records) {
				v = records[i]
				i++
			} else {
				return exportResult{}, nil
			}
		}
		cm := v.cacheManager
		key := cm.getID(v.key)
		res, err := cm.backend.Load(key, v.ID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				v = nil
				continue
			}
			return exportResult{}, err
		}
		r.record = v
		addResult := func(remote *Remote) {
			r.results = append(r.results, CacheExportResult{
				CreatedAt:  v.CreatedAt,
				Result:     remote,
				EdgeVertex: k.vtx,
				EdgeIndex:  k.output,
			})
		}

		remotes, err := cm.results.LoadRemotes(ctx, res, opt.CompressionOpt, opt.Session)
		if err != nil {
			return exportResult{}, err
		}
		if len(remotes) > 0 {
			r.remote, remotes = remotes[0], remotes[1:] // pop the first element
		}
		if opt.CompressionOpt != nil {
			for _, remote := range remotes { // record all remaining remotes as well
				addResult(remote)
			}
		}

		if needsLocalResult(r.remote, opt) && opt.Mode != CacheExportModeRemoteOnly {
			res, err := cm.results.Load(ctx, res)
			if err != nil {
				if !errors.Is(err, cerrdefs.ErrNotFound) {
					return exportResult{}, err
				}
				r.remote = nil
			} else {
				remotes, err := opt.ResolveRemotes(ctx, res)
				if err != nil {
					return exportResult{}, err
				}
				res.Release(context.TODO())
				if r.remote == nil && len(remotes) > 0 {
					r.remote, remotes = remotes[0], remotes[1:] // pop the first element
				}
				if opt.CompressionOpt != nil {
					for _, remote := range remotes { // record all remaining remotes as well
						addResult(remote)
					}
				}
			}
		}

		if r.remote != nil {
			addResult(r.remote)
		}
		return r, nil
	}
}

// needsLocalResult reports whether ExportTo must resolve a record's remote
// from its local result rather than export the remote it already has.
// Loading a result materializes it in the local cache, which is costly for
// every imported record of a mode=max export. Resolving only changes the blobs
// of an existing remote when compression is forced and a layer doesn't have it
// yet (see getRemote), so it is needed when there is no remote or for that
// conversion.
func needsLocalResult(remote *Remote, opt CacheExportOpt) bool {
	if remote == nil {
		return true
	}
	comp := opt.CompressionOpt
	if comp == nil || !comp.Force {
		return false
	}
	// eStargz and gzip layers share a media type, so only their content tells
	// them apart: a forced conversion between them always resolves.
	if comp.Type == compression.Gzip || comp.Type == compression.EStargz {
		return true
	}
	for _, desc := range remote.Descriptors {
		if !compression.IsMediaType(comp.Type, desc.MediaType) {
			return true
		}
	}
	return false
}

func getBestResult(records []*CacheRecord) *CacheRecord {
	records = slices.Clone(records)
	slices.SortStableFunc(records, compareCacheRecord)
	if len(records) == 0 {
		return nil
	}
	return records[0]
}

func compareCacheRecord(a, b *CacheRecord) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return 1
	}
	if b == nil {
		return -1
	}
	if v := b.CreatedAt.Compare(a.CreatedAt); v != 0 {
		return v
	}
	return a.Priority - b.Priority
}

type mergedExporter struct {
	exporters []CacheExporter
}

func (e *mergedExporter) ExportTo(ctx context.Context, t CacheExporterTarget, opt CacheExportOpt) (er []CacheExporterRecord, err error) {
	for _, e := range e.exporters {
		r, err := e.ExportTo(ctx, t, opt)
		if err != nil {
			return nil, err
		}
		er = append(er, r...)
	}
	return
}
