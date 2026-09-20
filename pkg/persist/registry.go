package persist

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// BackendType identifies a persist backend implementation (fs, goleveldb,
// redis, mongo, ...). It is the key of the backend registry; config
// validation and New both resolve through the same table, so a typo can
// never become a silent fallback to another backend.
type BackendType string

// BackendFS is the built-in filesystem backend. It is the default when
// PersistConfig.Backend is empty.
const BackendFS BackendType = "fs"

// BackendFactory constructs a Persist from a PersistConfig. Each backend
// (B2 goleveldb, B4 redis/etcd, B5 mongo/mysql/postgres, B6 oss, B8 webdav)
// contributes exactly one factory; a backend package only writes its
// storage mapping behind this signature.
type BackendFactory func(cfg PersistConfig) (Persist, error)

var (
	backendMu sync.RWMutex
	backends  = map[BackendType]BackendFactory{}
)

// RegisterBackend adds f to the backend registry under t. It panics on a
// duplicate registration or a nil factory: backend selection is 约束面, so
// two factories competing for one name, or a missing one, must fail loudly
// at startup rather than silently shadow each other.
func RegisterBackend(t BackendType, f BackendFactory) {
	if t == "" {
		panic("persist: RegisterBackend with empty backend type")
	}
	if f == nil {
		panic(fmt.Sprintf("persist: RegisterBackend(%q) with nil factory", t))
	}
	backendMu.Lock()
	defer backendMu.Unlock()
	if _, ok := backends[t]; ok {
		panic(fmt.Sprintf("persist: backend %q registered twice", t))
	}
	backends[t] = f
}

// IsBackendRegistered reports whether t has a registered factory. The
// config layer uses this to reject registry-external backends at startup,
// keeping config validation and persist.New on a single source of truth.
func IsBackendRegistered(t BackendType) bool {
	backendMu.RLock()
	defer backendMu.RUnlock()
	_, ok := backends[t]
	return ok
}

// SupportedBackends returns the sorted names of all registered backends,
// suitable for diagnostics and error messages.
func SupportedBackends() []string {
	backendMu.RLock()
	defer backendMu.RUnlock()
	out := make([]string, 0, len(backends))
	for t := range backends {
		out = append(out, string(t))
	}
	sort.Strings(out)
	return out
}

// registerBackends is the single, centralized registration point for all
// persist backends (约束面可见): the full set of backends is visible in one
// place. B4 (redis/etcd), B5 (mongo/mysql/postgres), B6 (oss) and B8 (webdav)
// each add one RegisterBackend line here.
func registerBackends() {
	RegisterBackend(BackendFS, func(cfg PersistConfig) (Persist, error) {
		return NewFSPersist(filepath.Join(cfg.DataDir, cfg.Prefix)), nil
	})
	RegisterBackend(BackendLevelDB, newLevelDBPersist)
	RegisterBackend(BackendRedis, newRedisPersist)
	RegisterBackend(BackendETCD, newEtcdPersist)
	RegisterBackend(BackendOSS, newOSSPersist)
	RegisterBackend(BackendWebDAV, newWebDAVPersist)
	RegisterBackend(BackendMongo, newMongoPersist)
	RegisterBackend(BackendMySQL, func(cfg PersistConfig) (Persist, error) {
		return newSQLPersist(cfg, mysqlDialect{})
	})
	RegisterBackend(BackendPostgres, func(cfg PersistConfig) (Persist, error) {
		return newSQLPersist(cfg, postgresDialect{})
	})
}

func init() {
	registerBackends()
}

// joinSupported renders the registered backend names for error messages.
func joinSupported() string {
	return strings.Join(SupportedBackends(), ", ")
}
