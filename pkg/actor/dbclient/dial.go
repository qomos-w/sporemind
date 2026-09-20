package dbclient

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"github.com/redis/go-redis/v9"
	"github.com/studio-b12/gowebdav"

	"github.com/qomos-w/sporemind/pkg/persist"
)

// dialBackendConn is the production dial function installed in OnStart. It
// resolves the credential through the process-wide resolver (so rotated
// profiles take effect on reconnect) and the tunnel through
// persist.ResolveTunnel, then dispatches to the per-backend driver. Each
// path mirrors pkg/persist/<backend>.go's dial logic exactly so the
// decision-point-4 TLS rule (tunnel=>off, direct=>on unless loopback) and
// the explicit-scheme override apply to dbclient with no drift.
func dialBackendConn(ctx context.Context, p profileSummary, dbKey string) (backendConn, error) {
	cred, err := persist.ResolveCredential(p.ID)
	if err != nil {
		return nil, err
	}
	switch p.Backend {
	case "redis":
		return dialRedis(ctx, p, dbKey, cred)
	case "etcd":
		return dialEtcd(ctx, p, cred)
	case "mongo":
		return dialMongo(ctx, p, cred)
	case "mysql":
		return dialSQL(ctx, p, dbKey, cred, mysqlDialectImpl{})
	case "postgres":
		return dialSQL(ctx, p, dbKey, cred, postgresDialectImpl{})
	case "oss":
		return dialOSS(ctx, p, cred)
	case "webdav":
		return dialWebDAV(ctx, p, cred)
	default:
		return nil, fmt.Errorf("dbclient: unsupported backend %q", p.Backend)
	}
}

// --- redis ---

func dialRedis(ctx context.Context, p profileSummary, dbKey string, cred persist.Credential) (backendConn, error) {
	ep := strings.TrimSpace(p.Endpoint)
	if ep == "" {
		return nil, fmt.Errorf("dbclient: redis profile %q requires Endpoint", p.ID)
	}
	opts := &redis.Options{DialTimeout: dialTimeout}
	var explicitTLS *bool
	if strings.Contains(ep, "://") {
		u, err := redis.ParseURL(ep)
		if err != nil {
			return nil, fmt.Errorf("dbclient: redis endpoint %q: %w", p.Endpoint, err)
		}
		opts.Addr = u.Addr
		opts.TLSConfig = u.TLSConfig
		explicitTLS = boolPtr(u.TLSConfig != nil)
	} else {
		opts.Addr = ep
	}
	if p.TunnelRef != "" {
		local, err := persist.ResolveTunnel(p.TunnelRef, opts.Addr)
		if err != nil {
			return nil, err
		}
		opts.Addr = local
	}
	opts.Username = cred.Username
	opts.Password = cred.Password
	if dbKey != "" {
		n, err := strconv.Atoi(dbKey)
		if err != nil {
			return nil, fmt.Errorf("dbclient: redis db index %q: %w", dbKey, err)
		}
		opts.DB = n
	}
	if explicitTLS == nil {
		opts.TLSConfig = dbClientTLS(p)
	} else if *explicitTLS && opts.TLSConfig == nil {
		opts.TLSConfig = dbClientTLS(p)
	}
	c := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	if err := c.Ping(pingCtx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("dbclient: redis ping %s: %w", opts.Addr, err)
	}
	db := 0
	if opts.DB > 0 {
		db = opts.DB
	}
	return &redisConn{c: c, db: db}, nil
}

func boolPtr(b bool) *bool { return &b }

func dbClientTLS(p profileSummary) *tls.Config {
	if !persist.BackendTLSFor(p.Endpoint, p.TunnelRef) {
		return nil
	}
	return &tls.Config{MinVersion: tls.VersionTLS12}
}

// --- etcd ---

func dialEtcd(ctx context.Context, p profileSummary, cred persist.Credential) (backendConn, error) {
	ep := strings.TrimSpace(p.Endpoint)
	if ep == "" {
		return nil, fmt.Errorf("dbclient: etcd profile %q requires Endpoint", p.ID)
	}
	tlsOn := persist.BackendTLSFor(p.Endpoint, p.TunnelRef)
	if strings.HasPrefix(ep, "http://") {
		ep = strings.TrimPrefix(ep, "http://")
		tlsOn = false
	}
	if strings.HasPrefix(ep, "https://") {
		ep = strings.TrimPrefix(ep, "https://")
		tlsOn = true
	}
	if p.TunnelRef != "" {
		local, err := persist.ResolveTunnel(p.TunnelRef, ep)
		if err != nil {
			return nil, err
		}
		ep = local
	}
	conf := clientv3.Config{
		Endpoints:   []string{ep},
		DialTimeout: 5 * time.Second,
		Username:    cred.Username,
		Password:    cred.Password,
	}
	if tlsOn {
		conf.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	c, err := clientv3.New(conf)
	if err != nil {
		return nil, fmt.Errorf("dbclient: etcd dial %s: %w", ep, err)
	}
	stCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := c.Status(stCtx, conf.Endpoints[0]); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("dbclient: etcd status %s: %w", conf.Endpoints[0], err)
	}
	return &etcdConn{c: c}, nil
}

// --- mongo ---

func dialMongo(ctx context.Context, p profileSummary, cred persist.Credential) (backendConn, error) {
	host, port, err := net.SplitHostPort(p.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("dbclient: mongo Endpoint %q must be host:port: %w", p.Endpoint, err)
	}
	tlsOn := persist.BackendTLSFor(p.Endpoint, p.TunnelRef)
	dialAddr := net.JoinHostPort(host, port)
	if p.TunnelRef != "" {
		local, terr := persist.ResolveTunnel(p.TunnelRef, p.Endpoint)
		if terr != nil {
			return nil, terr
		}
		dialAddr = local
		tlsOn = false
	}
	dh, dp, err := net.SplitHostPort(dialAddr)
	if err != nil {
		return nil, fmt.Errorf("dbclient: mongo dial addr %q: %w", dialAddr, err)
	}
	clientOpts := options.Client().
		SetHosts([]string{net.JoinHostPort(dh, dp)}).
		SetConnectTimeout(10 * time.Second).
		SetServerSelectionTimeout(10 * time.Second).
		SetMaxPoolSize(16)
	if cred.Username != "" {
		clientOpts.SetAuth(options.Credential{
			Username:   cred.Username,
			Password:   cred.Password,
			AuthSource: "admin",
		})
	}
	if tlsOn {
		clientOpts.SetTLSConfig(&tls.Config{ServerName: dh, MinVersion: tls.VersionTLS12})
	}
	client, err := mongo.Connect(clientOpts)
	if err != nil {
		return nil, fmt.Errorf("dbclient: mongo connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		_ = client.Disconnect(pingCtx)
		return nil, fmt.Errorf("dbclient: mongo ping %s: %w", dialAddr, err)
	}
	return &mongoConn{client: client, dbHint: p.Database}, nil
}

// --- sql (mysql + postgres) ---

// Register pgx as database/sql "pgx" so the dialect keeps using a single
// stable driver name. The driver is registered in pgx/v5/stdlib's package
// init(); nothing extra needed here. The blank import keeps go vet honest
// about the dependency.
var _ = stdlib.GetDefaultDriver

var (
	dbClientMysqlTLSMu sync.Mutex
	dbClientMysqlTLS   = map[string]string{}
)

func dialSQL(ctx context.Context, p profileSummary, dbKey string, cred persist.Credential, d sqlDialect) (backendConn, error) {
	host, port, err := net.SplitHostPort(p.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("dbclient: sql Endpoint %q must be host:port: %w", p.Endpoint, err)
	}
	tlsOn := persist.BackendTLSFor(p.Endpoint, p.TunnelRef)
	dialAddr := net.JoinHostPort(host, port)
	if p.TunnelRef != "" {
		local, terr := persist.ResolveTunnel(p.TunnelRef, p.Endpoint)
		if terr != nil {
			return nil, terr
		}
		dialAddr = local
		tlsOn = false
	}
	dh, dp, err := net.SplitHostPort(dialAddr)
	if err != nil {
		return nil, fmt.Errorf("dbclient: sql dial addr %q: %w", dialAddr, err)
	}
	effectiveDB := dbKey
	if effectiveDB == "" {
		effectiveDB = p.Database
	}
	var dsn string
	switch d.(type) {
	case mysqlDialectImpl:
		tlsName := ""
		if tlsOn {
			tlsName = dbClientRegisterMySQLTLS(dh)
		}
		b := strings.Builder{}
		fmt.Fprintf(&b, "%s:%s@tcp(%s)/%s?parseTime=true&multiStatements=false",
			cred.Username, cred.Password,
			net.JoinHostPort(dh, dp),
			effectiveDB)
		if tlsName != "" {
			fmt.Fprintf(&b, "&tls=%s", tlsName)
		}
		dsn = b.String()
	case postgresDialectImpl:
		sslmode := "disable"
		if tlsOn {
			sslmode = "verify-full"
		}
		dsn = fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=%s&application_name=sporemind",
			postgresURLEscape(cred.Username), postgresURLEscape(cred.Password),
			net.JoinHostPort(dh, dp), effectiveDB, sslmode)
	default:
		return nil, fmt.Errorf("dbclient: unknown sql dialect %T", d)
	}
	db, err := sql.Open(d.driverName(), dsn)
	if err != nil {
		return nil, fmt.Errorf("dbclient: sql open (%s): %w", d.driverName(), err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("dbclient: sql ping %s (%s): %w", dialAddr, d.driverName(), err)
	}
	return &sqlConn{db: db, dialect: d, boundDB: effectiveDB}, nil
}

func dbClientRegisterMySQLTLS(host string) string {
	dbClientMysqlTLSMu.Lock()
	defer dbClientMysqlTLSMu.Unlock()
	if name, ok := dbClientMysqlTLS[host]; ok {
		return name
	}
	name := "sporemind-dbclient-" + sanitizeTLSName(host)
	_ = mysql.RegisterTLSConfig(name, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	dbClientMysqlTLS[host] = name
	return name
}

func sanitizeTLSName(host string) string {
	r := strings.NewReplacer(":", "_", ".", "_", "/", "_")
	return r.Replace(host)
}

func postgresURLEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, r := range s {
		if 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '-' || r == '_' || r == '.' || r == '~' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[(r>>4)&0xF])
		b.WriteByte(hex[r&0xF])
	}
	return b.String()
}

// --- oss ---

func dialOSS(ctx context.Context, p profileSummary, cred persist.Credential) (backendConn, error) {
	epURL, err := persist.ParseOSSEndpoint(p.Endpoint)
	if err != nil {
		return nil, err
	}
	static := credentials.NewStaticV4(cred.AccessKey, cred.Secret, "")
	dialTarget := epURL.DialTarget()
	var transport http.RoundTripper
	if p.TunnelRef != "" {
		local, terr := persist.ResolveTunnel(p.TunnelRef, dialTarget)
		if terr != nil {
			return nil, terr
		}
		dialTarget = local
		if epURL.Secure {
			transport = persist.TunnelTransport(epURL.Host)
		}
	}
	opts := &minio.Options{
		Creds:        static,
		Secure:       epURL.Secure,
		Transport:    transport,
		Region:       "",
		BucketLookup: minio.BucketLookupPath,
	}
	client, err := minio.New(dialTarget, opts)
	if err != nil {
		return nil, fmt.Errorf("dbclient: oss client: %w", err)
	}
	client.SetAppInfo("sporemind", "dbclient-oss")
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := client.ListBuckets(pingCtx); err != nil {
		return nil, fmt.Errorf("dbclient: oss ListBuckets %s: %w", dialTarget, err)
	}
	return &ossConn{c: client, bucket: p.Database}, nil
}

// --- webdav ---

func dialWebDAV(ctx context.Context, p profileSummary, cred persist.Credential) (backendConn, error) {
	parsed, err := url.Parse(p.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("dbclient: webdav parse endpoint %q: %w", p.Endpoint, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("dbclient: webdav endpoint scheme must be http or https, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("dbclient: webdav endpoint must include a host")
	}
	var client *gowebdav.Client
	switch {
	case cred.Token != "":
		client = gowebdav.NewAuthClient(parsed.String(), gowebdav.NewPreemptiveAuth(bearerAuth{token: cred.Token}))
	case cred.Username != "" || cred.Password != "":
		client = gowebdav.NewClient(parsed.String(), cred.Username, cred.Password)
	default:
		client = gowebdav.NewClient(parsed.String(), "", "")
	}
	if p.TunnelRef != "" {
		localAddr, terr := persist.ResolveTunnel(p.TunnelRef, parsed.Host)
		if terr != nil {
			return nil, terr
		}
		tlsConf := &tls.Config{ServerName: parsed.Hostname(), MinVersion: tls.VersionTLS12}
		client.SetTransport(&http.Transport{
			DialContext: func(dctx context.Context, _, _ string) (net.Conn, error) {
				d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
				return d.DialContext(dctx, "tcp", localAddr)
			},
			TLSClientConfig:   tlsConf,
			ForceAttemptHTTP2: true,
		})
	}
	// gowebdav has no context-aware Stat — the actor-level dialTimeout
	// wraps the call but the client cannot honor a per-op deadline. We
	// accept this trade-off for the in-process webdav test path; production
	// dials hang for at most a TCP connect timeout (10s default).
	if _, err := client.Stat("/"); err != nil {
		return nil, fmt.Errorf("dbclient: webdav ping %s: %w", parsed.String(), err)
	}
	return &webdavConn{c: client}, nil
}

// bearerAuth is a gowebdav.Authenticator that sends a Bearer token.
type bearerAuth struct{ token string }

func (b bearerAuth) Authorize(_ *http.Client, rq *http.Request, _ string) error {
	if b.token != "" {
		rq.Header.Set("Authorization", "Bearer "+b.token)
	}
	return nil
}

func (b bearerAuth) Verify(_ *http.Client, rs *http.Response, _ string) (bool, error) {
	if rs.StatusCode == 401 {
		return false, fmt.Errorf("dbclient: webdav bearer auth rejected: 401")
	}
	return false, nil
}

func (b bearerAuth) Close() error { return nil }
func (b bearerAuth) Clone() gowebdav.Authenticator {
	return bearerAuth{token: b.token}
}
