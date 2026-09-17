package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	opspostgres "github.com/TokenFlux/TokenRouter/internal/ops/postgres"

	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/app/bootstrap"
	"github.com/TokenFlux/TokenRouter/internal/config"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

// run 在返回错误前完成已取得资源的释放。
func run() error {
	beforeRaw := flag.String("before", "", "required RFC3339 cutoff; only older rows are considered")
	execute := flag.Bool("execute", false, "delete matched rows (default is dry-run)")
	batchSize := flag.Int("batch-size", 5000, "scan/delete batch size (1-5000)")
	flag.Parse()

	if *beforeRaw == "" {
		return fmt.Errorf("--before is required")
	}
	before, err := time.Parse(time.RFC3339, *beforeRaw)
	if err != nil {
		return fmt.Errorf("invalid --before: %v", err)
	}
	if *batchSize < 1 || *batchSize > 5000 {
		return fmt.Errorf("--batch-size must be between 1 and 5000")
	}

	cfg, err := config.LoadForBootstrap()
	if err != nil {
		return fmt.Errorf("load config: %v", err)
	}
	client, db, err := bootstrap.InitEnt(context.Background(), cfg)
	if err != nil {
		return fmt.Errorf("initialize database: %v", err)
	}
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	counts, scanned, matched, deleted, err := ops.CleanupHistoricalIngress(ctx, opspostgres.NewHistoricalIngressCleanup(db), before, *batchSize, *execute)
	if err != nil {
		return fmt.Errorf("cleanup failed: %v", err)
	}

	digest := sha256.Sum256([]byte(ops.HistoricalIngressClassifierVersion))
	mode := "dry-run"
	if *execute {
		mode = "execute"
	}
	fmt.Printf("mode=%s before=%s classifier=%s scanned=%d matched=%d deleted=%d\n",
		mode, before.UTC().Format(time.RFC3339), hex.EncodeToString(digest[:]), scanned, matched, deleted)
	reasons := make([]string, 0, len(counts))
	for reason := range counts {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		fmt.Printf("reason=%s count=%d\n", reason, counts[reason])
	}
	if *execute && deleted > 0 {
		fmt.Println("cleanup complete; schedule VACUUM (ANALYZE) ops_error_logs during normal maintenance")
	}
	return nil
}
