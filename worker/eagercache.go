package worker

import (
	"context"
	"os"
	"sync"
	"time"

	cacheconfig "github.com/moby/buildkit/cache/config"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/compression"
)

const (
	eagerZstdCacheEnv       = "BUILDKIT_EAGER_ZSTD_CACHE_EXPORT"
	eagerZstdQueueSize      = 32
	eagerZstdWorkerCount    = 2
	eagerZstdPrepareTimeout = 10 * time.Minute
)

type eagerZstdRequest struct {
	id  string
	ref *WorkerRef
}

var (
	eagerZstdOnce    sync.Once
	eagerZstdQueue   chan eagerZstdRequest
	eagerZstdPending sync.Map
)

func enqueueEagerZstdPreparation(ref *WorkerRef) {
	if os.Getenv(eagerZstdCacheEnv) != "1" || ref.ImmutableRef == nil {
		return
	}

	id := ref.ID()
	if _, loaded := eagerZstdPending.LoadOrStore(id, struct{}{}); loaded {
		return
	}

	eagerZstdOnce.Do(func() {
		eagerZstdQueue = make(chan eagerZstdRequest, eagerZstdQueueSize)
		for range eagerZstdWorkerCount {
			go eagerZstdWorker()
		}
	})

	eagerRef := &WorkerRef{ImmutableRef: ref.ImmutableRef.Clone(), Worker: ref.Worker}
	select {
	case eagerZstdQueue <- eagerZstdRequest{id: id, ref: eagerRef}:
	default:
		eagerZstdPending.Delete(id)
		_ = eagerRef.Release(context.Background())
	}
}

func eagerZstdWorker() {
	for req := range eagerZstdQueue {
		prepareEagerZstd(req)
	}
}

func prepareEagerZstd(req eagerZstdRequest) {
	defer eagerZstdPending.Delete(req.id)
	defer req.ref.Release(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), eagerZstdPrepareTimeout)
	defer cancel()

	refCfg := cacheconfig.RefConfig{
		Compression: compression.New(compression.Zstd).SetLevel(1),
	}
	if _, err := req.ref.GetRemotes(ctx, true, refCfg, false, nil); err != nil {
		bklog.G(ctx).WithError(err).Debug("failed to eagerly prepare zstd cache blob")
	}
}
