// busbench 在一个进程中启动多个消息总线实例模拟多台服务器，按群消息扇出与 AI 回复同步的流量形状逐级加压并运行带 NOTIFY 的业务写事务，输出各级的送达延迟、丢失、发送失败、业务提交延迟与 PostgreSQL 通知队列使用率。
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"github.com/runforyou-ai/luway/pkg/clusterbus"
)

const (
	// deltaKind 是运行流增量的单向消息种类。
	deltaKind = "bench.delta"
	// deltaInterval 是运行流增量的发送周期，与 Agent 运行流的合并发布周期一致。
	deltaInterval = 50 * time.Millisecond
	// tickInterval 是负载生成器的调度周期。
	tickInterval = 10 * time.Millisecond
	// drainWait 是每一级停止发送后等待在途消息送达的时长。
	drainWait = 3 * time.Second
	// wakeChannel 是业务写事务 NOTIFY 的频道，模拟任务入队唤醒。
	wakeChannel = "busbench_wake"
)

// options 是一次压测的参数。
type options struct {
	driver       string
	instances    int
	levels       []float64
	duration     time.Duration
	members      int
	perMessage   int
	runsPerLevel float64
	watchers     int
	payloadBytes int
	deltaBytes   int
	writeRate    float64
	stall        bool
	channel      string
}

// main 解析参数，启动总线实例并逐级执行压测。
func main() {
	var opts options
	var levels string
	flag.StringVar(&opts.driver, "driver", clusterbus.DriverPostgres, "传输层：postgres 或 nats（进程内 NATS 服务器）")
	flag.IntVar(&opts.instances, "instances", 2, "模拟的服务器台数")
	flag.StringVar(&levels, "levels", "1,2,4,8,16,32,64", "逐级的负载倍数，逗号分隔")
	flag.DurationVar(&opts.duration, "duration", 20*time.Second, "每一级的持续时长")
	flag.IntVar(&opts.members, "members", 20, "每个群的成员数，一条群消息给每个成员各发若干条通知")
	flag.IntVar(&opts.perMessage, "per-message", 3, "一条群消息给每个成员产生的通知数（会话变更、输入开始与结束）")
	flag.Float64Var(&opts.runsPerLevel, "runs", 1, "每倍负载同时生成中的 AI 回复数")
	flag.IntVar(&opts.watchers, "watchers", 1, "每条 AI 回复同步到的其他服务器台数")
	flag.IntVar(&opts.payloadBytes, "payload-bytes", 240, "一条通知的内容长度")
	flag.IntVar(&opts.deltaBytes, "delta-bytes", 320, "一条运行流增量的内容长度")
	flag.Float64Var(&opts.writeRate, "write-rate", 100, "每秒带 NOTIFY 的业务写事务数，0 表示不运行")
	flag.BoolVar(&opts.stall, "stall", false, "额外登记一个监听但不读取的连接，模拟卡住的服务器")
	flag.StringVar(&opts.channel, "channel", "busbench", "PostgreSQL 总线的广播频道")
	flag.Parse()
	for _, field := range strings.Split(levels, ",") {
		level, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err != nil {
			fail("负载倍数无法解析：%v", err)
		}
		opts.levels = append(opts.levels, level)
	}
	counters := &logCounters{}
	slog.SetDefault(slog.New(counters))
	if err := run(opts, counters); err != nil {
		fail("%v", err)
	}
}

// fail 输出错误并退出。
func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// connConfig 按 POSTGRES_* 环境变量生成 PostgreSQL 连接配置。
func connConfig() *pgx.ConnConfig {
	databaseURL := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("POSTGRES_USER"), os.Getenv("POSTGRES_PASSWORD")),
		Host:     net.JoinHostPort(os.Getenv("POSTGRES_HOST"), os.Getenv("POSTGRES_PORT")),
		Path:     os.Getenv("POSTGRES_DB"),
		RawQuery: url.Values{"sslmode": {os.Getenv("POSTGRES_SSLMODE")}}.Encode(),
	}
	config, err := pgx.ParseConfig(databaseURL.String())
	if err != nil {
		fail("解析 PostgreSQL 连接配置失败：%v", err)
	}
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return config
}

// openDB 按 POSTGRES_* 环境变量连接 PostgreSQL，连接池上限为 maxConns。
func openDB(maxConns int) *bun.DB {
	db := bun.NewDB(stdlib.OpenDB(*connConfig()), pgdialect.New())
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	return db
}

// node 是一个模拟的服务器实例。
type node struct {
	bus *clusterbus.Bus
	db  *bun.DB
	// topics 是连接到本实例的成员的通知主题。
	topics map[string]struct{}
}

// bench 保存压测的实例、成员分布与统计。
type bench struct {
	opts     options
	nodes    []*node
	users    []string
	counters *logCounters
	// levels 是各级负载的统计，消息内容携带所属级别，迟到的消息计入发送时的级别。
	levels []*levelStats
}

// run 启动全部实例与可选的卡住连接，逐级压测并输出结果。
func run(opts options, counters *logCounters) error {
	b := &bench{opts: opts, counters: counters}
	var nats string
	if opts.driver == clusterbus.DriverNATS {
		server := natstest.RunRandClientPortServer()
		defer server.Shutdown()
		nats = server.ClientURL()
	}
	statsDB := openDB(4)
	defer statsDB.Close()
	// 成员数为群成员数的若干倍，每个成员连接到一台服务器，订阅自己的通知主题。
	userCount := max(opts.members*8, opts.instances)
	for index := range opts.instances {
		n := &node{topics: map[string]struct{}{}}
		id := fmt.Sprintf("bench%02d", index)
		if opts.driver == clusterbus.DriverNATS {
			n.bus = clusterbus.NewNATS(id, clusterbus.NATSOptions{URL: nats, Namespace: "busbench"})
		} else {
			n.db = openDB(4)
			n.bus = clusterbus.NewPostgres(id, n.db.DB, connConfig(), clusterbus.PostgresOptions{Channel: opts.channel})
		}
		b.nodes = append(b.nodes, n)
	}
	for index := range userCount {
		user := fmt.Sprintf("realtime.org.user.%08d", index)
		b.users = append(b.users, user)
		b.nodes[index%opts.instances].topics[user] = struct{}{}
	}
	for _, n := range b.nodes {
		b.register(n)
		if err := n.bus.Start(); err != nil {
			return fmt.Errorf("start bus: %w", err)
		}
		defer func() {
			_ = n.bus.Close()
			if n.db != nil {
				_ = n.db.Close()
			}
		}()
	}
	for _, n := range b.nodes {
		if err := n.bus.Sync(context.Background()); err != nil {
			return fmt.Errorf("sync bus: %w", err)
		}
	}
	if opts.stall {
		conn, err := statsDB.Conn(context.Background())
		if err != nil {
			return err
		}
		defer conn.Close()
		if _, err := conn.ExecContext(context.Background(), "LISTEN "+opts.channel); err != nil {
			return err
		}
	}
	writeDB := openDB(16)
	defer writeDB.Close()
	if opts.writeRate > 0 {
		if _, err := writeDB.ExecContext(context.Background(), "CREATE TABLE IF NOT EXISTS busbench_writes (id bigserial PRIMARY KEY, payload text NOT NULL, created_at timestamptz NOT NULL DEFAULT now())"); err != nil {
			return err
		}
		defer writeDB.ExecContext(context.Background(), "DROP TABLE IF EXISTS busbench_writes")
		// 中断时同样删除业务写事务使用的表。
		interrupted := make(chan os.Signal, 1)
		signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-interrupted
			_, _ = writeDB.ExecContext(context.Background(), "DROP TABLE IF EXISTS busbench_writes")
			os.Exit(1)
		}()
	}
	fmt.Printf("driver=%s instances=%d members=%d per-message=%d runs/level=%.1f watchers=%d payload=%dB delta=%dB write-rate=%.0f/s stall=%t duration=%s\n",
		opts.driver, opts.instances, opts.members, opts.perMessage, opts.runsPerLevel, opts.watchers, opts.payloadBytes, opts.deltaBytes, opts.writeRate, opts.stall, opts.duration)
	fmt.Println("msg/s  runs  notify/s  bytes/s    deliv/s  bc.p50  bc.p99  bc.max  dl.p50  dl.p99  lost%   sendErr drops  wr.p50  wr.p99  queue%")
	levels := append([]float64{0}, opts.levels...)
	b.levels = make([]*levelStats, len(levels))
	for index := range levels {
		b.levels[index] = &levelStats{}
	}
	for index, level := range levels {
		result := b.runLevel(index, level, writeDB, statsDB)
		fmt.Println(result)
	}
	return nil
}

// register 为实例登记成员通知与运行流增量的接收统计。
func (b *bench) register(n *node) {
	for topic := range n.topics {
		if _, err := n.bus.Subscribe(topic, func(data []byte) { b.received(false, data) }); err != nil {
			fail("subscribe: %v", err)
		}
	}
	n.bus.HandleMessage(deltaKind, func(_ string, data []byte) { b.received(true, data) })
}

// runLevel 按负载倍数发送群消息通知与运行流增量并运行业务写事务，等待在途消息送达后汇总结果。
func (b *bench) runLevel(index int, level float64, writeDB, statsDB *bun.DB) string {
	stats := b.levels[index]
	b.counters.reset()
	ctx, cancel := context.WithTimeout(context.Background(), b.opts.duration)
	defer cancel()
	var wg sync.WaitGroup
	if b.opts.writeRate > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.runWrites(ctx, writeDB, stats)
		}()
	}
	runs := int(level * b.opts.runsPerLevel)
	for run := range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.runStream(ctx, index, run)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.runMessages(ctx, index, level)
	}()
	start := time.Now()
	wg.Wait()
	elapsed := time.Since(start)
	time.Sleep(drainWait)
	var usage float64
	_ = statsDB.QueryRowContext(context.Background(), "SELECT pg_notification_queue_usage()").Scan(&usage)
	if b.opts.driver == clusterbus.DriverNATS {
		usage = 0
	}
	return stats.format(level, b.opts.runsPerLevel, elapsed, b.counters, usage)
}

// runMessages 以每倍负载每秒一条的速率在随机实例上发出群消息，按成员扇出广播通知。
func (b *bench) runMessages(ctx context.Context, index int, level float64) {
	if level == 0 {
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	start := time.Now()
	sent := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		due := int(time.Since(start).Seconds() * level)
		for ; sent < due; sent++ {
			b.sendMessage(index)
		}
	}
}

// sendMessage 从随机群成员中选出接收方，逐条广播一条群消息的全部通知。
func (b *bench) sendMessage(index int) {
	// 接收方都连接在其他实例上，延迟只统计经传输层的送达；传输层负载与本机是否有接收方无关。
	source := rand.IntN(len(b.nodes))
	from := b.nodes[source]
	topics := make([]string, 0, b.opts.members*b.opts.perMessage)
	for len(topics) < cap(topics) {
		index := rand.IntN(len(b.users))
		if len(b.nodes) > 1 && index%len(b.nodes) == source {
			continue
		}
		for range b.opts.perMessage {
			topics = append(topics, b.users[index])
		}
	}
	for _, topic := range topics {
		data := encodePayload(index, b.opts.payloadBytes)
		b.levels[index].sent(&b.levels[index].broadcast, from.bus.Broadcast(topic, data), len(data), 1)
	}
}

// runStream 在一个实例上模拟一条生成中的 AI 回复，按增量周期发给其他实例中的镜像。
func (b *bench) runStream(ctx context.Context, index, run int) {
	source := run % len(b.nodes)
	var targets []string
	for offset := 1; offset <= b.opts.watchers && offset < len(b.nodes); offset++ {
		targets = append(targets, b.nodes[(source+offset)%len(b.nodes)].bus.ID())
	}
	// 各回复的发送时刻错开，模拟独立开始的回复。
	time.Sleep(time.Duration(rand.Int64N(int64(deltaInterval))))
	ticker := time.NewTicker(deltaInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, target := range targets {
			data := encodePayload(index, b.opts.deltaBytes)
			b.levels[index].sent(&b.levels[index].direct, b.nodes[source].bus.Send(target, deltaKind, data), len(data), 1)
		}
	}
}

// runWrites 按设定速率并发执行写入一行并 NOTIFY 的业务事务，记录提交耗时。
func (b *bench) runWrites(ctx context.Context, db *bun.DB, stats *levelStats) {
	interval := time.Duration(float64(time.Second) / b.opts.writeRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			started := time.Now()
			err := db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
				if _, err := tx.ExecContext(ctx, "INSERT INTO busbench_writes (payload) VALUES (?)", strings.Repeat("x", 200)); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, "SELECT pg_notify(?, 'pool')", wakeChannel)
				return err
			})
			stats.write(time.Since(started), err)
		}()
	}
}

// payloadSequence 是消息内容的递增序号。
var payloadSequence atomic.Int64

// encodePayload 生成以负载级别与发送时刻开头、补足到指定长度的消息内容。
func encodePayload(level, size int) []byte {
	// 发送时刻后附序号，同一事务中内容相同的 NOTIFY 会被合并为一条。
	header := strconv.Itoa(level) + "." + strconv.FormatInt(time.Now().UnixNano(), 10) + "." + strconv.FormatInt(payloadSequence.Add(1), 10) + "|"
	return append([]byte(header), bytes.Repeat([]byte("x"), max(0, size-len(header)))...)
}

// latencies 是一类消息的送达统计。
type latencies struct {
	mu       sync.Mutex
	values   []time.Duration
	expected atomic.Int64
}

// snapshot 返回已记录延迟的有序副本。
func (l *latencies) snapshot() []time.Duration {
	l.mu.Lock()
	values := slices.Clone(l.values)
	l.mu.Unlock()
	slices.Sort(values)
	return values
}

// levelStats 是一级负载的统计。
type levelStats struct {
	broadcast latencies
	direct    latencies
	messages  atomic.Int64
	bytes     atomic.Int64
	sendErr   atomic.Int64
	writesMu  sync.Mutex
	writes    []time.Duration
	writeErr  atomic.Int64
}

// sent 记录一次发送：成功时计入消息数、字节数与该类消息的预期送达数，失败时计入发送失败。
func (s *levelStats) sent(target *latencies, err error, size, deliveries int) {
	if err != nil {
		s.sendErr.Add(1)
		return
	}
	s.messages.Add(1)
	s.bytes.Add(int64(size))
	target.expected.Add(int64(deliveries))
}

// received 按消息开头的负载级别与发送时刻，把一次广播或单向消息的送达延迟计入对应级别。
func (b *bench) received(direct bool, data []byte) {
	header, _, _ := bytes.Cut(data, []byte("|"))
	fields := strings.Split(string(header), ".")
	index, err := strconv.Atoi(fields[0])
	if err != nil {
		return
	}
	nanos, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return
	}
	target := &b.levels[index].broadcast
	if direct {
		target = &b.levels[index].direct
	}
	latency := time.Since(time.Unix(0, nanos))
	target.mu.Lock()
	target.values = append(target.values, latency)
	target.mu.Unlock()
}

// write 记录一次业务写事务的耗时。
func (s *levelStats) write(duration time.Duration, err error) {
	if err != nil {
		s.writeErr.Add(1)
		return
	}
	s.writesMu.Lock()
	s.writes = append(s.writes, duration)
	s.writesMu.Unlock()
}

// percentile 返回已排序耗时的分位值。
func percentile(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	return values[min(len(values)-1, int(float64(len(values))*p))]
}

// format 汇总一级的发送速率、送达延迟、丢失率、失败与业务提交延迟。
func (s *levelStats) format(level, runsPerLevel float64, elapsed time.Duration, counters *logCounters, queueUsage float64) string {
	seconds := elapsed.Seconds()
	broadcast, direct := s.broadcast.snapshot(), s.direct.snapshot()
	s.writesMu.Lock()
	writes := slices.Clone(s.writes)
	s.writesMu.Unlock()
	slices.Sort(writes)
	delivered := int64(len(broadcast) + len(direct))
	expected := s.broadcast.expected.Load() + s.direct.expected.Load()
	lost := 0.0
	if expected > 0 {
		lost = 100 * float64(expected-delivered) / float64(expected)
	}
	return fmt.Sprintf("%5.0f  %4d  %8.0f  %9s  %7.0f  %6s  %6s  %6s  %6s  %6s  %6.2f  %7d %5d  %6s  %6s  %6.4f",
		level, int(level*runsPerLevel), float64(s.messages.Load())/seconds, humanBytes(float64(s.bytes.Load())/seconds), float64(delivered)/seconds,
		short(percentile(broadcast, 0.5)), short(percentile(broadcast, 0.99)), short(percentile(broadcast, 1)),
		short(percentile(direct, 0.5)), short(percentile(direct, 0.99)),
		lost, s.sendErr.Load()+s.writeErr.Load(), counters.drops.Load(),
		short(percentile(writes, 0.5)), short(percentile(writes, 0.99)), queueUsage*100)
}

// short 把耗时格式化为毫秒。
func short(d time.Duration) string {
	return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
}

// humanBytes 把字节速率格式化为 KB 或 MB。
func humanBytes(value float64) string {
	if value >= 1<<20 {
		return fmt.Sprintf("%.1fMB", value/(1<<20))
	}
	return fmt.Sprintf("%.0fKB", value/(1<<10))
}

// logCounters 统计总线丢弃与发送失败的日志，其余日志输出到标准错误。
type logCounters struct {
	drops atomic.Int64
}

// reset 清零计数。
func (c *logCounters) reset() {
	c.drops.Store(0)
}

// Enabled 只处理 Warn 及以上级别。
func (c *logCounters) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}

// Handle 把总线丢弃与发送失败计入统计，其余日志输出到标准错误。
func (c *logCounters) Handle(_ context.Context, record slog.Record) error {
	if strings.Contains(record.Message, "丢弃") || strings.Contains(record.Message, "发送失败") {
		c.drops.Add(1)
		return nil
	}
	fmt.Fprintln(os.Stderr, record.Level, record.Message)
	return nil
}

// WithAttrs 返回自身。
func (c *logCounters) WithAttrs([]slog.Attr) slog.Handler { return c }

// WithGroup 返回自身。
func (c *logCounters) WithGroup(string) slog.Handler { return c }
