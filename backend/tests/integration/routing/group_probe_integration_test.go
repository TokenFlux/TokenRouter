//go:build integration

package routing_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/stretchr/testify/require"
)

// 验证真实 PostgreSQL 租约竞争、到期回收、最终结果与下次时间的原子保存。
func TestS06GroupProbeLeaseAndAtomicResult(t *testing.T) {
	ctx := context.Background()
	client, integrationDB := routingDatabase(t)
	suffix := time.Now().UnixNano()
	group, err := client.Group.Create().SetName(fmt.Sprintf("s06-probe-%d", suffix)).
		SetPlatform(routing.PlatformOpenAI).
		SetAllowedProtocols(capability.DefaultGroupClientProtocols(routing.PlatformOpenAI)).
		SetProtocolFallbacks(capability.DefaultProtocolFallbacks(routing.PlatformOpenAI)).
		SetAvailabilityProbeConfig(routing.GroupAvailabilityProbeConfig{Enabled: true, IntervalMinutes: 5, ModelID: "gpt-test", TimeoutSeconds: 10}).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Group.DeleteOneID(group.ID).Exec(context.Background())) })
	repo := routingpostgres.NewGroupAvailabilityProbeRepository(integrationDB)
	now := time.Now().UTC()
	type claimResult struct {
		groups []routing.GroupAvailabilityProbeDueGroup
		err    error
	}
	claimed := make(chan claimResult, 2)
	for i := range 2 {
		go func(i int) {
			v, e := repo.ClaimDue(ctx, now, now.Add(time.Minute), fmt.Sprintf("runner-%d", i), 5)
			claimed <- claimResult{v, e}
		}(i)
	}
	matches := 0
	for range 2 {
		r := <-claimed
		require.NoError(t, r.err)
		for _, v := range r.groups {
			if v.GroupID == group.ID {
				matches++
			}
		}
	}
	require.Equal(t, 1, matches)
	later := now.Add(2 * time.Minute)
	recovered, err := repo.ClaimDue(ctx, later, later.Add(time.Minute), "recovered", 5)
	require.NoError(t, err)
	require.Contains(t, func() []int64 {
		ids := make([]int64, len(recovered))
		for i := range recovered {
			ids[i] = recovered[i].GroupID
		}
		return ids
	}(), group.ID)

	functionName := fmt.Sprintf("s06_probe_result_fail_%d", suffix)
	triggerName := fmt.Sprintf("s06_probe_result_trigger_%d", suffix)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.group_id = %d THEN RAISE EXCEPTION 's06 forced schedule failure'; END IF; RETURN NEW; END $$`, functionName, group.ID))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := integrationDB.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON group_availability_probe_states", triggerName))
		require.NoError(t, e)
		_, e = integrationDB.ExecContext(context.Background(), fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
		require.NoError(t, e)
	})
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT OR UPDATE ON group_availability_probe_states FOR EACH ROW EXECUTE FUNCTION %s()", triggerName, functionName))
	require.NoError(t, err)
	result := &routing.GroupAvailabilityProbeResult{GroupID: group.ID, ModelID: "gpt-test", Status: routing.GroupAvailabilityProbeStatusSuccess, Success: true, LatencyMs: 15, StartedAt: now, FinishedAt: now.Add(15 * time.Millisecond)}
	next := later.Add(5 * time.Minute)
	require.ErrorContains(t, repo.SaveResultAndScheduleNext(ctx, result, next), "s06 forced schedule failure")
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM group_availability_probe_results WHERE group_id=$1", group.ID).Scan(&count))
	require.Zero(t, count)
	var owner string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT locked_by FROM group_availability_probe_states WHERE group_id=$1", group.ID).Scan(&owner))
	require.Equal(t, "recovered", owner)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER %s ON group_availability_probe_states", triggerName))
	require.NoError(t, err)
	require.NoError(t, repo.SaveResultAndScheduleNext(ctx, result, next))
	var scheduled time.Time
	var released bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT next_run_at, locked_by IS NULL AND locked_until IS NULL FROM group_availability_probe_states WHERE group_id=$1", group.ID).Scan(&scheduled, &released))
	require.True(t, released)
	require.WithinDuration(t, next, scheduled, time.Microsecond)
	summaries, err := repo.GetSummaryByGroupIDs(ctx, []int64{group.ID}, 1, 120, "Asia/Shanghai", later)
	require.NoError(t, err)
	require.EqualValues(t, 1, summaries[group.ID].SuccessCount)
	require.EqualValues(t, 1, summaries[group.ID].TotalCount)
}
