package dbclient

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"github.com/redis/go-redis/v9"
	"github.com/studio-b12/gowebdav"
	"github.com/minio/minio-go/v7"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// objectReadCap is the upper bound for a single ObjectRead response (matches
// sshmanager.file_download's 50 MiB cap so a misbehaving caller cannot ask
// the gateway to base64 a 10 GB object into one callable payload).
const objectReadCap int64 = 50 * 1024 * 1024

// ossDirectorySuffix is the zero-byte marker S3-compatible object stores
// conventionally use to represent "directories". We create one whenever the
// caller asks ObjectMkdir on oss (no native dir concept), and the ObjectList
// implementation collapses any object matching "<prefix>/" into a single dir
// entry.
const ossDirectorySuffix = "/"

// --- redis ---

type redisConn struct {
	c  *redis.Client
	db int
}

func (c *redisConn) Ping(ctx context.Context) (string, error) {
	if err := c.c.Ping(ctx).Err(); err != nil {
		return "", err
	}
	info, err := c.c.Info(ctx, "server").Result()
	if err != nil {
		return "", nil // ping ok, version probe optional
	}
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "redis_version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "redis_version:")), nil
		}
	}
	return "", nil
}

func (c *redisConn) Tree(ctx context.Context, path, cursor string) ([]domain.DbTreeNode, string, bool, error) {
	if path == "" {
		return c.treeDBs(ctx), "", false, nil
	}
	idx := path
	if i := strings.IndexByte(idx, '/'); i >= 0 {
		idx = idx[:i]
	}
	wantDB, _ := strconv.Atoi(idx)
	if wantDB != c.db {
		return nil, "", false, fmt.Errorf("redis tree expects path %d, this handle is bound to db %d", wantDB, c.db)
	}
	cur := uint64(0)
	if cursor != "" {
		v, err := strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			return nil, "", false, fmt.Errorf("dbclient: invalid redis cursor %q", cursor)
		}
		cur = v
	}
	keys, next, err := c.c.Scan(ctx, cur, "*", int64(redisPageSize)).Result()
	if err != nil {
		return nil, "", false, fmt.Errorf("dbclient: redis scan: %w", err)
	}
	nodes := make([]domain.DbTreeNode, 0, len(keys))
	for _, k := range keys {
		nodes = append(nodes, domain.DbTreeNode{
			Path:  strconv.Itoa(c.db) + "/" + k,
			Kind:  "key",
			Label: k,
		})
	}
	if next != 0 {
		return nodes, strconv.FormatUint(next, 10), true, nil
	}
	return nodes, "", false, nil
}

// treeDBs enumerates redis logical databases. CONFIG GET databases is the
// authoritative source; managed redis often returns an error, in which case
// we fall back to the conventional 16.
func (c *redisConn) treeDBs(ctx context.Context) []domain.DbTreeNode {
	const defaultDBs = 16
	count := defaultDBs
	if m, err := c.c.ConfigGet(ctx, "databases").Result(); err == nil {
		if v, ok := m["databases"]; ok {
			if n, perr := strconv.Atoi(v); perr == nil && n > 0 && n <= 1024 {
				count = n
			}
		}
	}
	nodes := make([]domain.DbTreeNode, 0, count)
	for i := 0; i < count; i++ {
		nodes = append(nodes, domain.DbTreeNode{
			Path:  strconv.Itoa(i),
			Kind:  "database",
			Label: fmt.Sprintf("db%d", i),
		})
	}
	return nodes
}

func (c *redisConn) Read(ctx context.Context, path string, limit, offset int) (domain.DbRows, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	if offset < 0 {
		offset = 0
	}
	idx := strings.IndexByte(path, '/')
	if idx < 0 {
		return domain.DbRows{}, fmt.Errorf("dbclient: redis read expects path dbIndex/key, got %q", path)
	}
	wantDB, _ := strconv.Atoi(path[:idx])
	if wantDB != c.db {
		return domain.DbRows{}, fmt.Errorf("redis read path is bound to db %d, this handle is db %d", wantDB, c.db)
	}
	key := path[idx+1:]
	if key == "" {
		return domain.DbRows{}, fmt.Errorf("dbclient: redis read expects path dbIndex/key, got empty key")
	}
	typ, err := c.c.Type(ctx, key).Result()
	if err != nil {
		return domain.DbRows{}, fmt.Errorf("dbclient: redis TYPE %q: %w", key, err)
	}
	if typ == "none" {
		return domain.DbRows{Columns: []string{"property", "value"}, Rows: [][]string{{"exists", "false"}}, Truncated: false}, nil
	}
	ttl, _ := c.c.TTL(ctx, key).Result()
	rows := [][]string{{"type", typ}, {"ttl", formatDuration(ttl)}}
	switch typ {
	case "string":
		val, err := c.c.Get(ctx, key).Result()
		if err != nil && err.Error() != "redis: nil" {
			return domain.DbRows{}, err
		}
		rows = append(rows, []string{"value", val})
	case "list":
		vals, _ := c.c.LRange(ctx, key, int64(offset), int64(offset+limit-1)).Result()
		rows = append(rows, listIndexRows(vals, offset)...)
	case "set":
		vals, _ := c.c.SMembers(ctx, key).Result()
		rows = append(rows, indexRows(vals, offset, limit)...)
	case "zset":
		vals, _ := c.c.ZRangeWithScores(ctx, key, int64(offset), int64(offset+limit-1)).Result()
		rows = append(rows, zsetRows(vals)...)
	case "hash":
		vals, _ := c.c.HGetAll(ctx, key).Result()
		rows = append(rows, hashRows(vals, offset, limit)...)
	case "stream":
		n, _ := c.c.XLen(ctx, key).Result()
		vals, _ := c.c.XRange(ctx, key, "-", "+").Result()
		rows = append(rows, streamRows(vals, offset, limit)...)
		rows = append(rows, []string{"length", strconv.FormatInt(n, 10)})
	default:
		rows = append(rows, []string{"value", fmt.Sprintf("(unsupported type %q)", typ)})
	}
	return newRows([]string{"property", "value"}, rows, false), nil
}

func (c *redisConn) Query(ctx context.Context, text, mode string) (domain.DbRows, error) {
	mode = normalizeMode(mode, "cmd")
	if mode != "cmd" {
		return domain.DbRows{}, fmt.Errorf("dbclient: redis profile only supports cmd mode, got %q", mode)
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return domain.DbRows{}, fmt.Errorf("dbclient: empty redis command")
	}
	cmd := strings.ToUpper(fields[0])
	if !redisReadCmds[cmd] {
		return domain.DbRows{}, fmt.Errorf("dbclient: redis command %q is not on the read whitelist", cmd)
	}
	args := make([]any, len(fields)-1)
	for i, f := range fields[1:] {
		args[i] = f
	}
	reply, err := c.c.Do(ctx, append([]any{fields[0]}, args...)...).Result()
	if err != nil {
		return domain.DbRows{}, fmt.Errorf("dbclient: redis %s: %w", cmd, err)
	}
	return newRows([]string{"result"}, renderRedisReply(reply), false), nil
}

func (c *redisConn) Close() error { return c.c.Close() }

func listIndexRows(vals []string, offset int) [][]string {
	out := make([][]string, len(vals))
	for i, v := range vals {
		out[i] = []string{strconv.Itoa(offset + i), v}
	}
	return out
}
func indexRows(vals []string, offset, limit int) [][]string {
	if offset >= len(vals) {
		return nil
	}
	end := offset + limit
	if end > len(vals) {
		end = len(vals)
	}
	out := make([][]string, 0, end-offset)
	for i := offset; i < end; i++ {
		out = append(out, []string{strconv.Itoa(i), vals[i]})
	}
	return out
}
func zsetRows(vals []redis.Z) [][]string {
	out := make([][]string, len(vals))
	for i, z := range vals {
		out[i] = []string{fmt.Sprintf("%v", z.Member), fmt.Sprintf("%v", z.Score)}
	}
	return out
}
func hashRows(m map[string]string, offset, limit int) [][]string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	if offset >= len(keys) {
		return nil
	}
	end := offset + limit
	if end > len(keys) {
		end = len(keys)
	}
	out := make([][]string, 0, end-offset)
	for _, k := range keys[offset:end] {
		out = append(out, []string{k, m[k]})
	}
	return out
}
func streamRows(vals []redis.XMessage, offset, limit int) [][]string {
	if offset >= len(vals) {
		return nil
	}
	end := offset + limit
	if end > len(vals) {
		end = len(vals)
	}
	out := make([][]string, 0, end-offset)
	for _, m := range vals[offset:end] {
		fields := make([]string, 0, len(m.Values))
		for k, v := range m.Values {
			fields = append(fields, fmt.Sprintf("%s=%s", k, v))
		}
		out = append(out, []string{m.ID, strings.Join(fields, " ")})
	}
	return out
}

func formatDuration(d time.Duration) string {
	if d == -1 {
		return "no expiry"
	}
	if d == -2 {
		return "missing"
	}
	return d.String()
}

func renderRedisReply(v any) [][]string {
	switch t := v.(type) {
	case []any:
		out := make([][]string, 0, len(t))
		for _, x := range t {
			out = append(out, []string{stringifyRedisScalar(x)})
		}
		return out
	default:
		return [][]string{{stringifyRedisScalar(v)}}
	}
}

func stringifyRedisScalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case nil:
		return ""
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func normalizeMode(mode, def string) string {
	if mode == "" {
		return def
	}
	return mode
}

// redisReadCmds is the explicit read-only whitelist (约束面 显式). It
// hardens the design card's "blacklist KEYS/FLUSH*/CONFIG/DEBUG 等" guidance:
// whitelisting keeps every write / blocking / config-mutating command out
// without per-call inspection.
var redisReadCmds = map[string]bool{
	"GET": true, "MGET": true, "GETRANGE": true, "STRLEN": true,
	"EXISTS": true, "TYPE": true, "TTL": true, "PTTL": true,
	"HGET": true, "HMGET": true, "HGETALL": true, "HKEYS": true, "HVALS": true, "HLEN": true, "HEXISTS": true, "HSCAN": true,
	"LRANGE": true, "LLEN": true, "LINDEX": true,
	"SMEMBERS": true, "SISMEMBER": true, "SMISMEMBER": true, "SCARD": true, "SSCAN": true, "SRANDMEMBER": true,
	"ZRANGE": true, "ZREVRANGE": true, "ZRANGEBYSCORE": true, "ZREVRANGEBYSCORE": true, "ZRANGEBYLEX": true,
	"ZCARD": true, "ZSCORE": true, "ZMSCORE": true, "ZRANK": true, "ZREVRANK": true, "ZCOUNT": true, "ZLEXCOUNT": true, "ZSCAN": true,
	"XLEN": true, "XRANGE": true, "XREVRANGE": true, "XINFO": true, "XREAD": true,
	"PFCOUNT": true,
	"BITCOUNT": true, "BITPOS": true, "GETBIT": true,
	"DBSIZE": true, "SCAN": true, "TIME": true, "ECHO": true, "PING": true,
	"INFO": true, "SLOWLOG": true, "LATENCY": true,
	"COMMAND": true,
}

// --- etcd ---

type etcdConn struct {
	c *clientv3.Client
}

func (c *etcdConn) Ping(ctx context.Context) (string, error) {
	resp, err := c.c.Status(ctx, c.c.Endpoints()[0])
	if err != nil {
		return "", err
	}
	return resp.Version, nil
}

func (c *etcdConn) Tree(ctx context.Context, path, cursor string) ([]domain.DbTreeNode, string, bool, error) {
	if cursor != "" {
		return nil, "", false, fmt.Errorf("dbclient: etcd tree does not support cursor pagination; use dbclient.query with a deeper prefix")
	}
	prefix := path
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	resp, err := c.c.Get(ctx, prefix, clientv3.WithPrefix(), clientv3.WithKeysOnly(), clientv3.WithLimit(int64(treePageSize)))
	if err != nil {
		return nil, "", false, fmt.Errorf("dbclient: etcd range %q: %w", prefix, err)
	}
	seenDirs := map[string]struct{}{}
	nodes := make([]domain.DbTreeNode, 0)
	for _, kv := range resp.Kvs {
		remainder := strings.TrimPrefix(string(kv.Key), prefix)
		if i := strings.IndexByte(remainder, '/'); i >= 0 {
			dir := remainder[:i]
			if _, dup := seenDirs[dir]; dup {
				continue
			}
			seenDirs[dir] = struct{}{}
			nodes = append(nodes, domain.DbTreeNode{
				Path:  prefix + dir,
				Kind:  "dir",
				Label: dir,
			})
			continue
		}
		nodes = append(nodes, domain.DbTreeNode{
			Path:  string(kv.Key),
			Kind:  "key",
			Label: remainder,
		})
	}
	return nodes, "", resp.More, nil
}

func (c *etcdConn) Read(ctx context.Context, path string, limit, offset int) (domain.DbRows, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	if offset < 0 {
		offset = 0
	}
	gr, err := c.c.Get(ctx, path)
	if err != nil {
		return domain.DbRows{}, fmt.Errorf("dbclient: etcd get %q: %w", path, err)
	}
	if len(gr.Kvs) > 0 {
		rows := make([][]string, 0, len(gr.Kvs))
		for _, kv := range gr.Kvs {
			rows = append(rows, []string{string(kv.Key), string(kv.Value)})
		}
	rs, tr := rowsWithTruncation(rows, limit)
		return newRows([]string{"key", "value"}, rs, tr), nil
	}
	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	fetch := limit + offset + 1
	resp, err := c.c.Get(ctx, prefix, clientv3.WithPrefix(), clientv3.WithLimit(int64(fetch)))
	if err != nil {
		return domain.DbRows{}, fmt.Errorf("dbclient: etcd range %q: %w", prefix, err)
	}
	if len(resp.Kvs) == 0 {
		return domain.DbRows{}, fmt.Errorf("dbclient: etcd key %q not found", path)
	}
	all := make([][]string, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		all = append(all, []string{string(kv.Key), string(kv.Value)})
	}
	if offset >= len(all) {
		return newRows([]string{"key", "value"}, nil, false), nil
	}
	window := all[offset:]
	truncated := false
	if len(window) > limit {
		window = window[:limit]
		truncated = true
	}
	return newRows([]string{"key", "value"}, window, truncated), nil
}

func (c *etcdConn) Query(ctx context.Context, text, mode string) (domain.DbRows, error) {
	mode = normalizeMode(mode, "auto")
	if mode != "auto" && mode != "cmd" {
		return domain.DbRows{}, fmt.Errorf("dbclient: etcd profile only supports auto/cmd mode, got %q", mode)
	}
	prefix := strings.TrimSpace(text)
	if prefix == "" {
		return domain.DbRows{}, fmt.Errorf("dbclient: etcd query expects a key prefix")
	}
	return c.Read(ctx, strings.TrimSuffix(prefix, "/"), defaultLimit, 0)
}

func (c *etcdConn) Close() error { return c.c.Close() }

// --- mongo ---

type mongoConn struct {
	client *mongo.Client
	dbHint string
}

func (c *mongoConn) Ping(ctx context.Context) (string, error) {
	if err := c.client.Ping(ctx, readpref.Primary()); err != nil {
		return "", err
	}
	var bi struct {
		Version string `bson:"version"`
	}
	if err := c.client.Database("admin").RunCommand(ctx, bson.M{"buildInfo": 1}).Decode(&bi); err != nil {
		return "", nil
	}
	return bi.Version, nil
}

func (c *mongoConn) Tree(ctx context.Context, path, cursor string) ([]domain.DbTreeNode, string, bool, error) {
	if cursor != "" {
		return nil, "", false, fmt.Errorf("dbclient: mongo tree does not support cursor")
	}
	if path == "" {
		names, err := c.client.ListDatabaseNames(ctx, bson.M{})
		if err != nil {
			if c.dbHint != "" {
				return []domain.DbTreeNode{{Path: c.dbHint, Kind: "database", Label: c.dbHint}}, "", false, nil
			}
			return nil, "", false, fmt.Errorf("dbclient: mongo ListDatabaseNames: %w", err)
		}
		out := make([]domain.DbTreeNode, 0, len(names))
		for _, n := range names {
			out = append(out, domain.DbTreeNode{Path: n, Kind: "database", Label: n})
		}
		return out, "", false, nil
	}
	dbName := path
	if i := strings.IndexByte(dbName, '/'); i >= 0 {
		dbName = dbName[:i]
	}
	names, err := c.client.Database(dbName).ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return nil, "", false, fmt.Errorf("dbclient: mongo ListCollectionNames %q: %w", dbName, err)
	}
	out := make([]domain.DbTreeNode, 0, len(names))
	for _, n := range names {
		out = append(out, domain.DbTreeNode{Path: dbName + "." + n, Kind: "collection", Label: n})
	}
	return out, "", false, nil
}

func (c *mongoConn) Read(ctx context.Context, path string, limit, offset int) (domain.DbRows, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	if offset < 0 {
		offset = 0
	}
	dbName, collName := splitMongoPath(path)
	if collName == "" {
		return domain.DbRows{}, fmt.Errorf("dbclient: mongo read expects path db.collection, got %q", path)
	}
	coll := c.client.Database(dbName).Collection(collName)
	fetch := int64(limit + 1)
	cur, err := coll.Find(ctx, bson.M{}, options.Find().SetLimit(fetch).SetSkip(int64(offset)))
	if err != nil {
		return domain.DbRows{}, fmt.Errorf("dbclient: mongo find %q: %w", path, err)
	}
	defer cur.Close(ctx)
	var docs []bson.M
	if err := cur.All(ctx, &docs); err != nil {
		return domain.DbRows{}, fmt.Errorf("dbclient: mongo decode %q: %w", path, err)
	}
	truncated := false
	if len(docs) > limit {
		docs = docs[:limit]
		truncated = true
	}
	return mongoRows(docs, truncated), nil
}

func (c *mongoConn) Query(ctx context.Context, text, mode string) (domain.DbRows, error) {
	mode = normalizeMode(mode, "json")
	if mode != "json" {
		return domain.DbRows{}, fmt.Errorf("dbclient: mongo profile only supports json mode, got %q", mode)
	}
	var req struct {
		Collection string                 `json:"collection"`
		Filter     map[string]interface{} `json:"filter"`
		Aggregate  []interface{}          `json:"aggregate"`
		Limit      int                    `json:"limit"`
		Skip       int                    `json:"skip"`
	}
	if err := json.Unmarshal([]byte(text), &req); err != nil {
		return domain.DbRows{}, fmt.Errorf("dbclient: mongo query must be JSON: %w", err)
	}
	if req.Collection == "" {
		return domain.DbRows{}, fmt.Errorf("dbclient: mongo query requires `collection`")
	}
	dbName := c.dbHint
	if dbName == "" {
		return domain.DbRows{}, fmt.Errorf("dbclient: mongo profile requires a default database (set in profile.Database)")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	skip := req.Skip
	if skip < 0 {
		skip = 0
	}
	coll := c.client.Database(dbName).Collection(req.Collection)
	var (
		docs  []bson.M
		trunc bool
		err   error
	)
	switch {
	case len(req.Aggregate) > 0:
		// Prepend a $limit stage so we can detect truncation cheaply without
		// fetching the entire pipeline result.
		pipeline := append([]interface{}{bson.M{"$limit": int64(limit + 1)}}, req.Aggregate...)
		cur, ferr := coll.Aggregate(ctx, pipeline)
		if ferr != nil {
			return domain.DbRows{}, fmt.Errorf("dbclient: mongo aggregate: %w", ferr)
		}
		err = cur.All(ctx, &docs)
	default:
		filter := req.Filter
		if filter == nil {
			filter = bson.M{}
		}
		cur, ferr := coll.Find(ctx, filter, options.Find().SetLimit(int64(limit+1)).SetSkip(int64(skip)))
		if ferr != nil {
			return domain.DbRows{}, fmt.Errorf("dbclient: mongo find: %w", ferr)
		}
		err = cur.All(ctx, &docs)
	}
	if err != nil {
		return domain.DbRows{}, fmt.Errorf("dbclient: mongo query: %w", err)
	}
	if len(docs) > limit {
		docs = docs[:limit]
		trunc = true
	}
	return mongoRows(docs, trunc), nil
}

func (c *mongoConn) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.client.Disconnect(ctx)
}

func splitMongoPath(path string) (db, coll string) {
	i := strings.IndexByte(path, '.')
	if i < 0 {
		return path, ""
	}
	return path[:i], path[i+1:]
}

func mongoRows(docs []bson.M, truncated bool) domain.DbRows {
	cols := []string{}
	seen := map[string]bool{}
	for _, d := range docs {
		for k := range d {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
		if len(cols) >= 32 {
			break
		}
	}
	if len(cols) == 0 {
		return domain.DbRows{Columns: []string{"_doc"}, Rows: nil}
	}
	rows := make([][]string, len(docs))
	for i, d := range docs {
		row := make([]string, len(cols))
		for j, k := range cols {
			row[j] = stringifyBSON(d[k])
		}
		rows[i] = row
	}
	return domain.DbRows{Columns: cols, Rows: rows, Truncated: truncated}
}

func stringifyBSON(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bson.ObjectID:
		return t.Hex()
	case bson.DateTime:
		return t.Time().UTC().Format(time.RFC3339Nano)
	default:
		b, err := bson.MarshalExtJSON(v, false, false)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// --- sql (mysql + postgres) ---

type sqlDialect interface {
	placeholder(int) string
	driverName() string
}

type mysqlDialectImpl struct{}
type postgresDialectImpl struct{}

func (mysqlDialectImpl) placeholder(i int) string    { return "?" }
func (mysqlDialectImpl) driverName() string          { return "mysql" }
func (postgresDialectImpl) placeholder(i int) string { return "$" + strconv.Itoa(i) }
func (postgresDialectImpl) driverName() string       { return "pgx" }

type sqlConn struct {
	db      *sql.DB
	dialect sqlDialect
	boundDB string
}

func (c *sqlConn) Ping(ctx context.Context) (string, error) {
	q := "SELECT VERSION()"
	if _, ok := c.dialect.(postgresDialectImpl); ok {
		q = "SELECT version()"
	}
	row := c.db.QueryRowContext(ctx, q)
	var v string
	if err := row.Scan(&v); err != nil {
		return "", err
	}
	return v, nil
}

func (c *sqlConn) Tree(ctx context.Context, path, cursor string) ([]domain.DbTreeNode, string, bool, error) {
	if cursor != "" {
		return nil, "", false, fmt.Errorf("dbclient: sql tree does not support cursor")
	}
	if path == "" {
		return c.treeDatabases(ctx), "", false, nil
	}
	return c.treeTables(ctx, path), "", false, nil
}

func (c *sqlConn) treeDatabases(ctx context.Context) []domain.DbTreeNode {
	_, isPg := c.dialect.(postgresDialectImpl)
	if isPg {
		rows, err := c.db.QueryContext(ctx, "SELECT datname FROM pg_database WHERE datallowconn ORDER BY datname")
		if err != nil {
			return nil
		}
		defer rows.Close()
		var out []domain.DbTreeNode
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err == nil {
				out = append(out, domain.DbTreeNode{Path: n, Kind: "database", Label: n})
			}
		}
		return out
	}
	rows, err := c.db.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []domain.DbTreeNode
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err == nil {
			out = append(out, domain.DbTreeNode{Path: n, Kind: "database", Label: n})
		}
	}
	return out
}

func (c *sqlConn) treeTables(ctx context.Context, dbName string) []domain.DbTreeNode {
	q := "SELECT table_name FROM information_schema.tables WHERE table_schema = " + c.dialect.placeholder(1) + " AND table_type IN ('BASE TABLE','VIEW') ORDER BY table_name"
	rows, err := c.db.QueryContext(ctx, q, dbName)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []domain.DbTreeNode
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err == nil {
			out = append(out, domain.DbTreeNode{Path: dbName + "." + n, Kind: "table", Label: n})
		}
	}
	return out
}

func (c *sqlConn) Read(ctx context.Context, path string, limit, offset int) (domain.DbRows, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	if offset < 0 {
		offset = 0
	}
	dbPart, tblPart := splitSQLPath(path)
	if tblPart == "" {
		return domain.DbRows{}, fmt.Errorf("dbclient: sql read expects path db.table or table, got %q", path)
	}
	if dbPart == "" {
		dbPart = c.boundDB
	}
	qualified := quoteSQLIdentifiers(c.dialect, dbPart, tblPart)
	q := "SELECT * FROM " + qualified + " LIMIT " + strconv.Itoa(limit+1) + " OFFSET " + strconv.Itoa(offset)
	return runSQLQuery(ctx, c.db, q)
}

func (c *sqlConn) Query(ctx context.Context, text, mode string) (domain.DbRows, error) {
	mode = normalizeMode(mode, "sql")
	if mode != "sql" {
		return domain.DbRows{}, fmt.Errorf("dbclient: sql profile only supports sql mode, got %q", mode)
	}
	if err := checkSQLAllowed(text); err != nil {
		return domain.DbRows{}, err
	}
	q := strings.TrimRight(strings.TrimSpace(text), ";")
	q += " LIMIT " + strconv.Itoa(defaultLimit+1)
	return runSQLQuery(ctx, c.db, q)
}

func (c *sqlConn) Close() error { return c.db.Close() }

// Describe returns columns + indexes for one table. The SQL is ported from
// the sporecloud sql-client describe_table handler (information_schema for
// columns; information_schema.statistics / pg_indexes for indexes).
func (c *sqlConn) Describe(ctx context.Context, path string) (domain.DbDescribeResp, error) {
	dbPart, tblPart := splitSQLPath(path)
	if tblPart == "" {
		return domain.DbDescribeResp{}, fmt.Errorf("dbclient: sql describe expects path db.table or table, got %q", path)
	}
	if dbPart == "" {
		dbPart = c.boundDB
	}
	_, isPg := c.dialect.(postgresDialectImpl)

	var cols []domain.DbColumnDef
	var idxs []domain.DbIndexDef
	if isPg {
		rows, err := c.db.QueryContext(ctx, `SELECT column_name, data_type, is_nullable='YES', COALESCE(column_default,'')
			FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name=$1 ORDER BY ordinal_position`, tblPart)
		if err != nil {
			return domain.DbDescribeResp{}, fmt.Errorf("describe %s: %w", tblPart, err)
		}
		for rows.Next() {
			var col domain.DbColumnDef
			if err := rows.Scan(&col.Name, &col.DataType, &col.Nullable, &col.Default); err != nil {
				rows.Close()
				return domain.DbDescribeResp{}, err
			}
			cols = append(cols, col)
		}
		rows.Close()

		rows, err = c.db.QueryContext(ctx, `SELECT indexname, indexdef FROM pg_indexes
			WHERE schemaname=current_schema() AND tablename=$1`, tblPart)
		if err != nil {
			return domain.DbDescribeResp{}, fmt.Errorf("describe %s indexes: %w", tblPart, err)
		}
		for rows.Next() {
			var name, def string
			if err := rows.Scan(&name, &def); err != nil {
				rows.Close()
				return domain.DbDescribeResp{}, err
			}
			idxs = append(idxs, domain.DbIndexDef{
				Name:    name,
				Columns: pgIndexColumns(def),
				Unique:  strings.HasPrefix(strings.ToUpper(strings.TrimSpace(def)), "CREATE UNIQUE"),
			})
		}
		rows.Close()
		return domain.DbDescribeResp{Columns: cols, Indexes: idxs}, nil
	}

	rows, err := c.db.QueryContext(ctx, `SELECT column_name, column_type, is_nullable='YES', COALESCE(column_default,''),
		COALESCE(column_key,''), COALESCE(extra,'')
		FROM information_schema.columns
		WHERE table_schema=? AND table_name=? ORDER BY ordinal_position`, dbPart, tblPart)
	if err != nil {
		return domain.DbDescribeResp{}, fmt.Errorf("describe %s: %w", tblPart, err)
	}
	for rows.Next() {
		var col domain.DbColumnDef
		if err := rows.Scan(&col.Name, &col.DataType, &col.Nullable, &col.Default, &col.Key, &col.Extra); err != nil {
			rows.Close()
			return domain.DbDescribeResp{}, err
		}
		cols = append(cols, col)
	}
	rows.Close()

	rows, err = c.db.QueryContext(ctx, `SELECT index_name, GROUP_CONCAT(column_name ORDER BY seq_in_index), MAX(NON_UNIQUE)=0
		FROM information_schema.statistics
		WHERE table_schema=? AND table_name=? GROUP BY index_name`, dbPart, tblPart)
	if err != nil {
		return domain.DbDescribeResp{}, fmt.Errorf("describe %s indexes: %w", tblPart, err)
	}
	for rows.Next() {
		var idx domain.DbIndexDef
		if err := rows.Scan(&idx.Name, &idx.Columns, &idx.Unique); err != nil {
			rows.Close()
			return domain.DbDescribeResp{}, err
		}
		idxs = append(idxs, idx)
	}
	rows.Close()
	return domain.DbDescribeResp{Columns: cols, Indexes: idxs}, nil
}

// pgIndexColumns extracts the ordered column list from a pg_indexes
// indexdef ("CREATE [UNIQUE] INDEX name ON tbl USING btree (a, b)").
func pgIndexColumns(def string) string {
	open := strings.Index(def, "(")
	close_ := strings.LastIndex(def, ")")
	if open < 0 || close_ <= open {
		return def
	}
	return def[open+1 : close_]
}

func splitSQLPath(path string) (db, table string) {
	i := strings.IndexByte(path, '.')
	if i < 0 {
		return "", path
	}
	return path[:i], path[i+1:]
}

func quoteSQLIdentifiers(d sqlDialect, parts ...string) string {
	q := "`"
	if _, ok := d.(postgresDialectImpl); ok {
		q = `"`
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, q+p+q)
	}
	return strings.Join(out, ".")
}

func runSQLQuery(ctx context.Context, db *sql.DB, q string) (domain.DbRows, error) {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return domain.DbRows{}, fmt.Errorf("dbclient: sql query: %w", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return domain.DbRows{}, fmt.Errorf("dbclient: sql columns: %w", err)
	}
	out := make([][]string, 0)
	for rows.Next() {
		holders := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range holders {
			ptrs[i] = &holders[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return domain.DbRows{}, fmt.Errorf("dbclient: sql scan: %w", err)
		}
		row := make([]string, len(cols))
		for i, h := range holders {
			row[i] = stringifySQLValue(h)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return domain.DbRows{}, fmt.Errorf("dbclient: sql rows: %w", err)
	}
	trunc := false
	if len(out) > defaultLimit {
		out = out[:defaultLimit]
		trunc = true
	}
	return newRows(cols, out, trunc), nil
}

func stringifySQLValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case string:
		return t
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// checkSQLAllowed enforces the read-only contract: single statement only,
// leading keyword restricted to SELECT/WITH/SHOW/EXPLAIN. Leading SQL
// comments (-- and /* */) are stripped before the keyword check so
// `  -- foo\n  SELECT 1` is allowed.
func checkSQLAllowed(text string) error {
	s := strings.TrimSpace(text)
	if s == "" {
		return fmt.Errorf("dbclient: empty sql query")
	}
	for {
		if strings.HasPrefix(s, "--") {
			idx := strings.IndexByte(s, '\n')
			if idx < 0 {
				return fmt.Errorf("dbclient: empty sql query")
			}
			s = strings.TrimSpace(s[idx+1:])
			continue
		}
		if strings.HasPrefix(s, "/*") {
			idx := strings.Index(s, "*/")
			if idx < 0 {
				return fmt.Errorf("dbclient: unterminated /* */ comment")
			}
			s = strings.TrimSpace(s[idx+2:])
			continue
		}
		break
	}
	if s == "" {
		return fmt.Errorf("dbclient: empty sql query")
	}
	s = strings.TrimSuffix(s, ";")
	if strings.ContainsRune(s, ';') {
		return fmt.Errorf("dbclient: multiple statements are not allowed")
	}
	upper := strings.ToUpper(firstWord(s))
	switch upper {
	case "SELECT", "WITH", "SHOW", "EXPLAIN":
		return nil
	}
	return fmt.Errorf("dbclient: only SELECT/WITH/SHOW/EXPLAIN statements are allowed, got %q", upper)
}

func firstWord(s string) string {
	for i, r := range s {
		if r <= ' ' || r == '(' {
			return s[:i]
		}
	}
	return s
}

// --- oss (s3-compatible) ---

type ossConn struct {
	c      *minio.Client
	bucket string
}

func (c *ossConn) Ping(ctx context.Context) (string, error) {
	if _, err := c.c.ListBuckets(ctx); err != nil {
		return "", err
	}
	return "", nil
}

func (c *ossConn) Tree(ctx context.Context, path, cursor string) ([]domain.DbTreeNode, string, bool, error) {
	if cursor != "" {
		return nil, "", false, fmt.Errorf("dbclient: oss tree does not support cursor")
	}
	if path == "" {
		buckets, err := c.c.ListBuckets(ctx)
		if err != nil {
			return nil, "", false, fmt.Errorf("dbclient: oss ListBuckets: %w", err)
		}
		out := make([]domain.DbTreeNode, 0, len(buckets))
		for _, b := range buckets {
			out = append(out, domain.DbTreeNode{
				Path:  b.Name,
				Kind:  "bucket",
				Label: b.Name,
				Meta:  map[string]string{"created": b.CreationDate.UTC().Format(time.RFC3339)},
			})
		}
		return out, "", false, nil
	}
	bucket, prefix := splitOSSBucketPath(path)
	seen := map[string]struct{}{}
	scanned := 0
	out := []domain.DbTreeNode{}
	for obj := range c.c.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, "", false, fmt.Errorf("dbclient: oss ListObjects: %w", obj.Err)
		}
		rest := strings.TrimPrefix(obj.Key, prefix)
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			dir := rest[:i]
			if _, dup := seen[dir]; dup {
				continue
			}
			seen[dir] = struct{}{}
			out = append(out, domain.DbTreeNode{
				Path:  bucket + "/" + prefix + dir,
				Kind:  "dir",
				Label: dir,
			})
			if len(out) >= treePageSize {
				return out, "", scanned < objectScanCap, nil
			}
		}
		scanned++
		if scanned >= objectScanCap {
			return out, "", true, nil
		}
	}
	return out, "", false, nil
}

func (c *ossConn) Read(ctx context.Context, path string, limit, offset int) (domain.DbRows, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	if offset < 0 {
		offset = 0
	}
	bucket, prefix := splitOSSBucketPath(path)
	rows := make([][]string, 0)
	scanned := 0
	skipped := 0
	for obj := range c.c.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return domain.DbRows{}, fmt.Errorf("dbclient: oss ListObjects: %w", obj.Err)
		}
		if scanned >= objectScanCap {
			rs, tr := rowsWithTruncation(rows, limit)
			return newRows([]string{"name", "size", "modified"}, rs, tr || true), nil
		}
		scanned++
		if skipped < offset {
			skipped++
			continue
		}
		rows = append(rows, []string{obj.Key, strconv.FormatInt(obj.Size, 10), obj.LastModified.UTC().Format(time.RFC3339)})
		if len(rows) >= limit+1 {
			rs, tr := rowsWithTruncation(rows, limit)
			return newRows([]string{"name", "size", "modified"}, rs, tr || true), nil
		}
	}
	return newRows([]string{"name", "size", "modified"}, rowsWithTruncationRowsOnly(rows, limit), false), nil
}

func (c *ossConn) Query(ctx context.Context, text, mode string) (domain.DbRows, error) {
	return domain.DbRows{}, fmt.Errorf("dbclient: oss profile is browse-only, query is not supported")
}

// --- oss object surface ---

func (c *ossConn) ObjectList(ctx context.Context, path, cursor string, limit int) ([]domain.DbObjectEntry, string, bool, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	if path == "" {
		// Bucket list is the same shape Tree() uses — re-use so the actor
		// layer has a single source of truth for "what is at the root".
		if cursor != "" {
			return nil, "", false, fmt.Errorf("dbclient: oss object_list does not support cursor on bucket root")
		}
		buckets, err := c.c.ListBuckets(ctx)
		if err != nil {
			return nil, "", false, fmt.Errorf("dbclient: oss ListBuckets: %w", err)
		}
		out := make([]domain.DbObjectEntry, 0, len(buckets))
		for _, b := range buckets {
			out = append(out, domain.DbObjectEntry{
				Name:      b.Name,
				Path:      b.Name,
				IsDir:     true,
				Size:      -1,
				Modified:  b.CreationDate.UTC().Format(time.RFC3339),
				ContentType: "application/x-directory",
			})
		}
		return out, "", false, nil
	}
	bucket, prefix := splitOSSBucketPath(path)
	opts := minio.ListObjectsOptions{Prefix: prefix, Recursive: false}
	if cursor != "" {
		opts.StartAfter = cursor
	}
	seenDirs := map[string]struct{}{}
	out := make([]domain.DbObjectEntry, 0)
	emitted := 0
	hasMore := false
	for obj := range c.c.ListObjects(ctx, bucket, opts) {
		if obj.Err != nil {
			return nil, "", false, fmt.Errorf("dbclient: oss ListObjects: %w", obj.Err)
		}
		rest := strings.TrimPrefix(obj.Key, prefix)
		// Skip the directory marker itself so it doesn't show up as a file.
		if rest == ossDirectorySuffix {
			continue
		}
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			dir := rest[:i]
			if _, dup := seenDirs[dir]; dup {
				continue
			}
			seenDirs[dir] = struct{}{}
			out = append(out, domain.DbObjectEntry{
				Name:        dir,
				Path:        bucket + "/" + prefix + dir,
				IsDir:       true,
				Size:        -1,
				ContentType: "application/x-directory",
			})
			emitted++
			if emitted >= limit {
				hasMore = true
				break
			}
			continue
		}
		ct := obj.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		out = append(out, domain.DbObjectEntry{
			Name:        rest,
			Path:        obj.Key,
			IsDir:       false,
			Size:        obj.Size,
			Modified:    obj.LastModified.UTC().Format(time.RFC3339),
			ContentType: ct,
		})
		emitted++
		if emitted >= limit {
			hasMore = true
			break
		}
	}
	nextCursor := ""
	if hasMore && len(out) > 0 {
		nextCursor = out[len(out)-1].Path
	}
	return out, nextCursor, hasMore, nil
}

func (c *ossConn) ObjectRead(ctx context.Context, path string, maxBytes int64) ([]byte, bool, string, string, error) {
	if maxBytes <= 0 || maxBytes > objectReadCap {
		maxBytes = objectReadCap
	}
	bucket, key := splitOSSBucketPath(path)
	if bucket == "" {
		return nil, false, "", "", fmt.Errorf("dbclient: oss object_read requires bucket in path")
	}
	stat, err := c.c.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, false, "", "", fmt.Errorf("dbclient: oss StatObject %q: %w", key, err)
	}
	if strings.HasSuffix(key, ossDirectorySuffix) {
		return nil, false, "", "", fmt.Errorf("dbclient: oss object_read: %q is a directory", path)
	}
	limit := stat.Size
	truncated := false
	if limit > maxBytes {
		limit = maxBytes
		truncated = true
	}
	obj, err := c.c.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, false, "", "", fmt.Errorf("dbclient: oss GetObject %q: %w", key, err)
	}
	defer obj.Close()
	buf := bytes.NewBuffer(make([]byte, 0, limit))
	if _, err := io.CopyN(buf, obj, limit); err != nil && err != io.EOF {
		return nil, false, "", "", fmt.Errorf("dbclient: oss GetObject read %q: %w", key, err)
	}
	ct := stat.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	return buf.Bytes(), truncated, ct, stat.LastModified.UTC().Format(time.RFC3339), nil
}

func (c *ossConn) ObjectWrite(ctx context.Context, path, contentType string, data []byte) (int64, error) {
	bucket, key := splitOSSBucketPath(path)
	if bucket == "" || key == "" {
		return 0, fmt.Errorf("dbclient: oss object_write requires <bucket>/<key> path")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := c.c.PutObject(ctx, bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return 0, fmt.Errorf("dbclient: oss PutObject %q: %w", key, err)
	}
	return int64(len(data)), nil
}

func (c *ossConn) ObjectDelete(ctx context.Context, path string) error {
	bucket, key := splitOSSBucketPath(path)
	if bucket == "" || key == "" {
		return fmt.Errorf("dbclient: oss object_delete requires <bucket>/<key> path")
	}
	// Recursive: enumerate everything under key, then feed minio a channel
	// of ObjectInfo and drain the error channel. minio's RemoveObjects API
	// is channel-based; we batch the listing first to size the channel so
	// the goroutine doesn't block on send.
	objectsCh := c.c.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: key, Recursive: true})
	keys := []string{}
	for obj := range objectsCh {
		if obj.Err != nil {
			return fmt.Errorf("dbclient: oss ListObjects for delete: %w", obj.Err)
		}
		keys = append(keys, obj.Key)
	}
	if len(keys) == 0 {
		return fmt.Errorf("dbclient: oss object_delete: %q not found", path)
	}
	src := make(chan minio.ObjectInfo, len(keys))
	for _, k := range keys {
		src <- minio.ObjectInfo{Key: k}
	}
	close(src)
	errCh := c.c.RemoveObjects(ctx, bucket, src, minio.RemoveObjectsOptions{})
	for res := range errCh {
		if res.Err != nil {
			return fmt.Errorf("dbclient: oss RemoveObjects %q: %w", res.ObjectName, res.Err)
		}
	}
	return nil
}

func (c *ossConn) ObjectMkdir(ctx context.Context, parent, name string) error {
	bucket, prefix := splitOSSBucketPath(parent)
	if bucket == "" {
		return fmt.Errorf("dbclient: oss object_mkdir requires bucket in parent path")
	}
	name = strings.TrimRight(name, "/")
	if name == "" {
		return fmt.Errorf("dbclient: oss object_mkdir: name is required")
	}
	marker := prefix + name + ossDirectorySuffix
	if strings.HasSuffix(marker, "//") {
		marker = strings.TrimSuffix(marker, "/")
	}
	_, err := c.c.PutObject(ctx, bucket, marker, bytes.NewReader(nil), 0, minio.PutObjectOptions{ContentType: "application/x-directory"})
	if err != nil {
		return fmt.Errorf("dbclient: oss mkdir marker %q: %w", marker, err)
	}
	return nil
}

func (c *ossConn) ObjectStat(ctx context.Context, path string) (int64, bool, string, string, error) {
	bucket, key := splitOSSBucketPath(path)
	if bucket == "" {
		return 0, false, "", "", fmt.Errorf("dbclient: oss object_stat requires bucket in path")
	}
	if strings.HasSuffix(key, ossDirectorySuffix) {
		return -1, true, "application/x-directory", "", nil
	}
	stat, err := c.c.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return 0, false, "", "", fmt.Errorf("dbclient: oss StatObject %q: %w", key, err)
	}
	ct := stat.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	return stat.Size, false, ct, stat.LastModified.UTC().Format(time.RFC3339), nil
}

func (c *ossConn) Close() error { return nil }

func splitOSSBucketPath(path string) (bucket, prefix string) {
	i := strings.IndexByte(path, '/')
	if i < 0 {
		return path, ""
	}
	return path[:i], path[i+1:]
}

// --- webdav ---

type webdavConn struct {
	c *gowebdav.Client
}

func (c *webdavConn) Ping(ctx context.Context) (string, error) {
	if _, err := c.c.Stat("/"); err != nil {
		return "", err
	}
	return "", nil
}

func (c *webdavConn) Tree(ctx context.Context, path, cursor string) ([]domain.DbTreeNode, string, bool, error) {
	if cursor != "" {
		return nil, "", false, fmt.Errorf("dbclient: webdav tree does not support cursor")
	}
	dir := "/" + strings.Trim(path, "/")
	fis, err := c.c.ReadDir(dir)
	if err != nil {
		return nil, "", false, fmt.Errorf("dbclient: webdav ReadDir %q: %w", dir, err)
	}
	out := make([]domain.DbTreeNode, 0, len(fis))
	for _, fi := range fis {
		name := fi.Name()
		child := strings.TrimRight(dir, "/") + "/" + name
		if fi.IsDir() {
			out = append(out, domain.DbTreeNode{Path: child, Kind: "dir", Label: name})
			continue
		}
		out = append(out, domain.DbTreeNode{
			Path:  child,
			Kind:  "object",
			Label: name,
			Meta: map[string]string{
				"size":  strconv.FormatInt(fi.Size(), 10),
				"mtime": fi.ModTime().UTC().Format(time.RFC3339),
			},
		})
	}
	return out, "", false, nil
}

func (c *webdavConn) Read(ctx context.Context, path string, limit, offset int) (domain.DbRows, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	if offset < 0 {
		offset = 0
	}
	p := "/" + strings.Trim(path, "/")
	fi, err := c.c.Stat(p)
	if err == nil && !fi.IsDir() {
		return newRows([]string{"property", "value"}, [][]string{
			{"name", fi.Name()},
			{"size", strconv.FormatInt(fi.Size(), 10)},
			{"modified", fi.ModTime().UTC().Format(time.RFC3339)},
			{"mode", fi.Mode().String()},
		}, false), nil
	}
	fis, err := c.c.ReadDir(p)
	if err != nil {
		return domain.DbRows{}, fmt.Errorf("dbclient: webdav ReadDir %q: %w", p, err)
	}
	all := make([][]string, 0, len(fis))
	for _, fi := range fis {
		all = append(all, []string{
			fi.Name(),
			strconv.FormatInt(fi.Size(), 10),
			fi.ModTime().UTC().Format(time.RFC3339),
			strconv.FormatBool(fi.IsDir()),
		})
	}
	if offset >= len(all) {
		return newRows([]string{"name", "size", "modified", "is_dir"}, nil, false), nil
	}
	window := all[offset:]
	truncated := false
	if len(window) > limit {
		window = window[:limit]
		truncated = true
	}
	return newRows([]string{"name", "size", "modified", "is_dir"}, window, truncated), nil
}

func (c *webdavConn) Query(ctx context.Context, text, mode string) (domain.DbRows, error) {
	return domain.DbRows{}, fmt.Errorf("dbclient: webdav profile is browse-only, query is not supported")
}

// --- webdav object surface ---

// webdavPath converts a logical dbclient path to the absolute WebDAV URL path
// (always rooted at "/", segments joined with "/", percent-escaped).
func webdavPath(raw string) (string, error) {
	cleaned := strings.Trim(raw, "/")
	parts := []string{}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == "" {
			continue
		}
		parts = append(parts, url.PathEscape(seg))
	}
	return "/" + path.Join(parts...), nil
}

func (c *webdavConn) ObjectList(ctx context.Context, rawPath, cursor string, limit int) ([]domain.DbObjectEntry, string, bool, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	if cursor != "" {
		return nil, "", false, fmt.Errorf("dbclient: webdav object_list does not support cursor")
	}
	p, err := webdavPath(rawPath)
	if err != nil {
		return nil, "", false, fmt.Errorf("dbclient: webdav path %q: %w", rawPath, err)
	}
	fis, err := c.c.ReadDir(p)
	if err != nil {
		return nil, "", false, fmt.Errorf("dbclient: webdav ReadDir %q: %w", p, err)
	}
	out := make([]domain.DbObjectEntry, 0, len(fis))
	for _, fi := range fis {
		name := fi.Name()
		child := strings.TrimRight(p, "/") + "/" + name
		if fi.IsDir() {
			out = append(out, domain.DbObjectEntry{
				Name:        name,
				Path:        strings.TrimPrefix(child, "/"),
				IsDir:       true,
				Size:        -1,
				ContentType: "application/x-directory",
			})
			continue
		}
		out = append(out, domain.DbObjectEntry{
			Name:        name,
			Path:        strings.TrimPrefix(child, "/"),
			IsDir:       false,
			Size:        fi.Size(),
			Modified:    fi.ModTime().UTC().Format(time.RFC3339),
			ContentType: "application/octet-stream",
		})
	}
	return out, "", false, nil
}

func (c *webdavConn) ObjectRead(ctx context.Context, rawPath string, maxBytes int64) ([]byte, bool, string, string, error) {
	if maxBytes <= 0 || maxBytes > objectReadCap {
		maxBytes = objectReadCap
	}
	p, err := webdavPath(rawPath)
	if err != nil {
		return nil, false, "", "", fmt.Errorf("dbclient: webdav path %q: %w", rawPath, err)
	}
	fi, err := c.c.Stat(p)
	if err != nil {
		return nil, false, "", "", fmt.Errorf("dbclient: webdav Stat %q: %w", p, err)
	}
	if fi.IsDir() {
		return nil, false, "", "", fmt.Errorf("dbclient: webdav object_read: %q is a directory", rawPath)
	}
	truncated := false
	size := fi.Size()
	reader, err := c.c.ReadStream(p)
	if err != nil {
		return nil, false, "", "", fmt.Errorf("dbclient: webdav ReadStream %q: %w", p, err)
	}
	defer reader.Close()
	limit := size
	if limit > maxBytes {
		limit = maxBytes
		truncated = true
	}
	buf := bytes.NewBuffer(make([]byte, 0, limit))
	if _, err := io.CopyN(buf, reader, limit); err != nil && err != io.EOF {
		return nil, false, "", "", fmt.Errorf("dbclient: webdav ReadStream copy %q: %w", p, err)
	}
	return buf.Bytes(), truncated, "application/octet-stream", fi.ModTime().UTC().Format(time.RFC3339), nil
}

func (c *webdavConn) ObjectWrite(ctx context.Context, rawPath, contentType string, data []byte) (int64, error) {
	p, err := webdavPath(rawPath)
	if err != nil {
		return 0, fmt.Errorf("dbclient: webdav path %q: %w", rawPath, err)
	}
	if err := c.c.Write(p, data, 0644); err != nil {
		return 0, fmt.Errorf("dbclient: webdav Write %q: %w", p, err)
	}
	return int64(len(data)), nil
}

func (c *webdavConn) ObjectDelete(ctx context.Context, rawPath string) error {
	p, err := webdavPath(rawPath)
	if err != nil {
		return fmt.Errorf("dbclient: webdav path %q: %w", rawPath, err)
	}
	fi, err := c.c.Stat(p)
	if err != nil {
		return fmt.Errorf("dbclient: webdav Stat %q: %w", p, err)
	}
	if fi.IsDir() {
		if err := c.c.RemoveAll(p); err != nil {
			return fmt.Errorf("dbclient: webdav RemoveAll %q: %w", p, err)
		}
		return nil
	}
	if err := c.c.Remove(p); err != nil {
		return fmt.Errorf("dbclient: webdav Remove %q: %w", p, err)
	}
	return nil
}

func (c *webdavConn) ObjectMkdir(ctx context.Context, rawParent, name string) error {
	name = strings.Trim(name, "/")
	if name == "" {
		return fmt.Errorf("dbclient: webdav object_mkdir: name is required")
	}
	parent, err := webdavPath(rawParent)
	if err != nil {
		return fmt.Errorf("dbclient: webdav path %q: %w", rawParent, err)
	}
	target := strings.TrimRight(parent, "/") + "/" + url.PathEscape(name)
	if err := c.c.Mkdir(target, 0755); err != nil {
		return fmt.Errorf("dbclient: webdav Mkdir %q: %w", target, err)
	}
	return nil
}

func (c *webdavConn) ObjectStat(ctx context.Context, rawPath string) (int64, bool, string, string, error) {
	p, err := webdavPath(rawPath)
	if err != nil {
		return 0, false, "", "", fmt.Errorf("dbclient: webdav path %q: %w", rawPath, err)
	}
	fi, err := c.c.Stat(p)
	if err != nil {
		return 0, false, "", "", fmt.Errorf("dbclient: webdav Stat %q: %w", p, err)
	}
	size := int64(-1)
	if !fi.IsDir() {
		size = fi.Size()
	}
	return size, fi.IsDir(), "application/octet-stream", fi.ModTime().UTC().Format(time.RFC3339), nil
}

func (c *webdavConn) Close() error { return nil }

// silence unused-import linter when future driver-specific helpers are dropped.
var (
	_ = options.Client
)
