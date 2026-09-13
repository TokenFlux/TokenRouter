package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	opspostgres "github.com/TokenFlux/TokenRouter/internal/ops/postgres"

	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/app/bootstrap"
	"github.com/TokenFlux/TokenRouter/internal/config"
)

func main() {
	beforeRaw := flag.String("before", "", "required RFC3339 cutoff; only older rows are considered")
	execute := flag.Bool("execute", false, "delete matched rows (default is dry-run)")
	batchSize := flag.Int("batch-size", 5000, "scan/delete batch size (1-5000)")
	flag.Parse()

	if *beforeRaw == "" {
		log.Fatal("--before is required")
	}
	before, err := time.Parse(time.RFC3339, *beforeRaw)
	if err != nil {
		log.Fatalf("invalid --before: %v", err)
	}
	if *batchSize < 1 || *batchSize > 5000 {
		log.Fatal("--batch-size must be between 1 and 5000")
	}

	cfg, err := config.LoadForBootstrap()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	client, db, err := bootstrap.InitEnt(context.Background(), cfg)
	if err != nil {
		log.Fatalf("initialize database: %v", err)
	}
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	counts, scanned, matched, deleted, err := ops.CleanupHistoricalIngress(ctx, opspostgres.NewHistoricalIngressCleanup(db), before, *batchSize, *execute)
	if err != nil {
		log.Fatalf("cleanup failed: %v", err)
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
}
