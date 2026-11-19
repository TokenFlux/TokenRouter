package ops

import (
	"context"
	"maps"
	"time"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// concurrency.go builds provider, group, platform, and user concurrency views
// from identities and scheduler loads.
// Live concurrency joins repository identities and configured capacities with
// separately fetched scheduler load counters. The same provider can belong to
// multiple groups, so group totals overlap, while provider details show a
// single selected or first membership. Platform and group ratios divide
// accumulated activity by accumulated configured capacity; they are not
// averages of detail percentages.
//
// Load queries deduplicate positive identities and run in bounded chunks.
// Failed chunks leave missing loads, which provider views display as zero
// activity while user views omit zero-activity rows. Scheduling factors used to
// request provider loads can differ from the displayed configured concurrency.
// Paginated identity reads and load queries advance separately, so collection
// timestamps do not establish a single atomic observation across these sources.

// Live statistics use bounded repository pages and smaller load-query chunks,
// so paginated identities and scheduler observations can be read in separate
// requests.
const (
	opsProvidersPageSize         = 100 // provider and user page size
	opsConcurrencyBatchChunkSize = 200 // identities per scheduler load query
)

// opsProviderStatsRepository is the optional direct reader for lightweight live
// provider observations.
type opsProviderStatsRepository interface {
	// ListOpsProvidersForStats reads provider observations for the supplied
	// platform and group selection. The service uses this optional method as a
	// complete result, bypassing paginated ProviderReader calls and returning its
	// query error directly.
	ListOpsProvidersForStats(ctx context.Context, platformFilter string, groupIDFilter *int64) ([]ProviderObservation, error)
}

// listAllProvidersForOps returns provider observations from the optional
// statistics reader or paginated ProviderReader. Nil service or absent reader
// yields an allocated empty slice. The paginated path forwards the platform and
// group value, stops at reported totals or a short/empty page, and returns the
// collected prefix after ten thousand pages; query errors discard that prefix.
func (s *OpsService) listAllProvidersForOps(ctx context.Context, platformFilter string, groupIDFilter *int64) ([]ProviderObservation, error) {
	if s == nil || s.providerRepo == nil {
		return []ProviderObservation{}, nil
	}
	// The optional statistics query already returns the needed provider
	// observations. Prefer it before pagination so these callers avoid the larger
	// page-oriented provider read.
	if repo, ok := s.providerRepo.(opsProviderStatsRepository); ok {
		return repo.ListOpsProvidersForStats(ctx, platformFilter, groupIDFilter)
	}

	out := make([]ProviderObservation, 0, 128)
	page := 1
	groupID := int64(0)
	if groupIDFilter != nil {
		groupID = *groupIDFilter
	}
	for {
		providers, pageInfo, err := s.providerRepo.ListPage(ctx, pagination.PaginationParams{
			Page:     page,
			PageSize: opsProvidersPageSize,
		}, platformFilter, groupID)
		if err != nil {
			return nil, err
		}
		if len(providers) == 0 {
			break
		}

		out = append(out, providers...)
		if pageInfo != nil && int64(len(out)) >= pageInfo.Total {
			break
		}
		if len(providers) < opsProvidersPageSize {
			break
		}

		page++
		// The ten-thousand-page limit returns a collected prefix when pagination
		// never provides a normal stopping point.
		if page > 10_000 {
			logger.LegacyPrintf("service.ops", "[Ops] listAllProvidersForOps: aborting after too many pages (platform=%q)", platformFilter)
			break
		}
	}

	return out, nil
}

// getProvidersLoadMapBestEffort returns merged scheduler load observations for
// positive provider identities, or an empty map when loading is unavailable.
// Duplicate IDs request their largest observed EffectiveLoadFactor, which can
// differ from the configured display concurrency.
//
// Requests run in chunks of two hundred. A failed chunk is logged and omitted
// while successful maps are merged as supplied, retaining their load pointers
// and allowing later returned keys to replace earlier values.
// GetConcurrencyStats keeps missing-load provider rows with zero activity and
// their configured capacity.
func (s *OpsService) getProvidersLoadMapBestEffort(ctx context.Context, providers []ProviderObservation) map[int64]*ProviderLoadInfo {
	if s == nil || s.concurrencyService == nil {
		return map[int64]*ProviderLoadInfo{}
	}
	if len(providers) == 0 {
		return map[int64]*ProviderLoadInfo{}
	}

	// Provider observations can repeat an identity. Request one load value using
	// the greatest observed scheduling factor, while display capacity remains the
	// configured concurrency.
	unique := make(map[int64]int, len(providers))
	for _, acc := range providers {
		if acc.ID <= 0 {
			continue
		}
		lf := acc.EffectiveLoadFactor()
		if prev, ok := unique[acc.ID]; !ok || lf > prev {
			unique[acc.ID] = lf
		}
	}

	batch := make([]ProviderWithConcurrency, 0, len(unique))
	for id, maxConc := range unique {
		batch = append(batch, ProviderWithConcurrency{
			ID:             id,
			MaxConcurrency: maxConc,
		})
	}

	out := make(map[int64]*ProviderLoadInfo, len(batch))
	for i := 0; i < len(batch); i += opsConcurrencyBatchChunkSize {
		end := min(i+opsConcurrencyBatchChunkSize, len(batch))
		part, err := s.concurrencyService.GetProvidersLoadBatch(ctx, batch[i:end])
		if err != nil {
			// Keep loads from successful chunks. Views apply their zero-activity
			// behavior to missing keys after this failed chunk is omitted.
			logger.LegacyPrintf("service.ops", "[Ops] GetProvidersLoadBatch failed: %v", err)
			continue
		}
		maps.Copy(out, part)
	}

	return out
}

// GetConcurrencyStats returns platform, group, and provider concurrency views
// after monitoring and provider-read checks. Platform filtering is delegated to
// the repository; a positive group filter is reapplied against returned
// memberships. Missing load entries keep selected providers present with zero
// activity and configured display capacity.
//
// Platform totals sum entries with a nonempty platform, while each eligible
// membership receives its provider load, so group totals can overlap. Repeated
// provider IDs keep the first provider detail but still contribute each entry
// to aggregate totals. Percentages use configured capacity when positive.
// CollectedAt is sampled before separate load queries, so the response is
// assembled from observations taken at different times.
func (s *OpsService) GetConcurrencyStats(
	ctx context.Context,
	platformFilter string,
	groupIDFilter *int64,
) (map[string]*PlatformConcurrencyInfo, map[int64]*GroupConcurrencyInfo, map[int64]*ProviderConcurrencyInfo, *time.Time, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, nil, nil, nil, err
	}

	providers, err := s.listAllProvidersForOps(ctx, platformFilter, groupIDFilter)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	collectedAt := time.Now()
	loadMap := s.getProvidersLoadMapBestEffort(ctx, providers)

	platform := make(map[string]*PlatformConcurrencyInfo)
	group := make(map[int64]*GroupConcurrencyInfo)
	provider := make(map[int64]*ProviderConcurrencyInfo)

	for _, acc := range providers {
		if acc.ID <= 0 {
			continue
		}

		var matchedGroup *GroupObservation
		if groupIDFilter != nil && *groupIDFilter > 0 {
			for _, grp := range acc.Groups {
				if grp == nil || grp.ID <= 0 {
					continue
				}
				if grp.ID == *groupIDFilter {
					matchedGroup = grp
					break
				}
			}
			// The paginated reader can return broader provider observations. Recheck the
			// selected group here so every displayed detail and aggregate follows that
			// membership filter.
			if matchedGroup == nil {
				continue
			}
		}

		load := loadMap[acc.ID]
		currentInUse := int64(0)
		waiting := int64(0)
		if load != nil {
			currentInUse = int64(load.CurrentConcurrency)
			waiting = int64(load.WaitingCount)
		}

		// Provider details have one display group even when aggregate membership
		// totals overlap. Use the selected membership or the first supplied
		// membership to label that detail.
		displayGroupID := int64(0)
		displayGroupName := ""
		if matchedGroup != nil {
			displayGroupID = matchedGroup.ID
			displayGroupName = matchedGroup.Name
		} else if len(acc.Groups) > 0 && acc.Groups[0] != nil {
			displayGroupID = acc.Groups[0].ID
			displayGroupName = acc.Groups[0].Name
		}

		if _, ok := provider[acc.ID]; !ok {
			info := &ProviderConcurrencyInfo{
				ProviderID:     acc.ID,
				ProviderName:   acc.Name,
				Platform:       acc.Platform,
				GroupID:        displayGroupID,
				GroupName:      displayGroupName,
				CurrentInUse:   currentInUse,
				MaxCapacity:    int64(acc.Concurrency),
				WaitingInQueue: waiting,
			}
			// Use displayed configured capacity here; load retrieval can use a different
			// effective scheduling factor for the same provider.
			if info.MaxCapacity > 0 {
				info.LoadPercentage = float64(info.CurrentInUse) / float64(info.MaxCapacity) * 100
			}
			provider[acc.ID] = info
		}

		// A nonempty saved platform places this entry in one platform total,
		// independently of how many eligible group memberships it contributes to.
		if acc.Platform != "" {
			if _, ok := platform[acc.Platform]; !ok {
				platform[acc.Platform] = &PlatformConcurrencyInfo{
					Platform: acc.Platform,
				}
			}
			p := platform[acc.Platform]
			p.MaxCapacity += int64(acc.Concurrency)
			p.CurrentInUse += currentInUse
			p.WaitingInQueue += waiting
		}

		// A selected group receives this entry once. Unfiltered observations
		// contribute to every positive membership, so adding group totals can count
		// the same provider several times.
		if matchedGroup != nil {
			grp := matchedGroup
			if _, ok := group[grp.ID]; !ok {
				group[grp.ID] = &GroupConcurrencyInfo{
					GroupID:   grp.ID,
					GroupName: grp.Name,
				}
			}
			g := group[grp.ID]
			if g.GroupName == "" && grp.Name != "" {
				g.GroupName = grp.Name
			}
			g.MaxCapacity += int64(acc.Concurrency)
			g.CurrentInUse += currentInUse
			g.WaitingInQueue += waiting
		} else {
			for _, grp := range acc.Groups {
				if grp == nil || grp.ID <= 0 {
					continue
				}
				if _, ok := group[grp.ID]; !ok {
					group[grp.ID] = &GroupConcurrencyInfo{
						GroupID:   grp.ID,
						GroupName: grp.Name,
					}
				}
				g := group[grp.ID]
				if g.GroupName == "" && grp.Name != "" {
					g.GroupName = grp.Name
				}
				g.MaxCapacity += int64(acc.Concurrency)
				g.CurrentInUse += currentInUse
				g.WaitingInQueue += waiting
			}
		}
	}

	// Platform and group percentages use ratios of their accumulated counters and
	// configured capacities, so they are not averages of individual provider
	// percentages.
	for _, info := range platform {
		// Nonpositive accumulated provider capacity keeps the displayed percentage at
		// zero.
		if info.MaxCapacity > 0 {
			info.LoadPercentage = float64(info.CurrentInUse) / float64(info.MaxCapacity) * 100
		}
	}
	for _, info := range group {
		if info.MaxCapacity > 0 {
			info.LoadPercentage = float64(info.CurrentInUse) / float64(info.MaxCapacity) * 100
		}
	}

	return platform, group, provider, &collectedAt, nil
}

// listAllActiveUsersForOps returns observations through the active-user
// paginated reader, using an allocated empty slice for a nil service or missing
// reader. It stops at the reported total or a short/empty page, returns a
// prefix after ten thousand pages, and propagates query errors without the
// collected prefix.
func (s *OpsService) listAllActiveUsersForOps(ctx context.Context) ([]UserObservation, error) {
	if s == nil || s.userRepo == nil {
		return []UserObservation{}, nil
	}

	out := make([]UserObservation, 0, 128)
	page := 1
	for {
		users, pageInfo, err := s.userRepo.ListActivePage(ctx, pagination.PaginationParams{
			Page:     page,
			PageSize: opsProvidersPageSize,
		})
		if err != nil {
			return nil, err
		}
		if len(users) == 0 {
			break
		}

		out = append(out, users...)
		if pageInfo != nil && int64(len(out)) >= pageInfo.Total {
			break
		}
		if len(users) < opsProvidersPageSize {
			break
		}

		page++
		// The ten-thousand-page limit returns a collected prefix when pagination
		// never provides a normal stopping point.
		if page > 10_000 {
			logger.LegacyPrintf("service.ops", "[Ops] listAllActiveUsersForOps: aborting after too many pages")
			break
		}
	}

	return out, nil
}

// getUsersLoadMapBestEffort returns merged scheduler loads for positive user
// identities, with empty results when loading is unavailable. Repeated IDs
// request the largest configured concurrency found in the supplied
// observations.
//
// The helper queries chunks of two hundred and omits an entire failed chunk
// after logging. Successful maps retain their load pointers, and later returned
// values overwrite repeated keys. GetUserConcurrencyStats converts missing
// entries to zero activity, which can omit users whose load was unavailable.
func (s *OpsService) getUsersLoadMapBestEffort(ctx context.Context, users []UserObservation) map[int64]*UserLoadInfo {
	if s == nil || s.concurrencyService == nil {
		return map[int64]*UserLoadInfo{}
	}
	if len(users) == 0 {
		return map[int64]*UserLoadInfo{}
	}

	// User observations can repeat an identity across pages. Request that identity
	// once using the greatest configured concurrency carried by those
	// observations.
	unique := make(map[int64]int, len(users))
	for _, u := range users {
		if u.ID <= 0 {
			continue
		}
		if prev, ok := unique[u.ID]; !ok || u.Concurrency > prev {
			unique[u.ID] = u.Concurrency
		}
	}

	batch := make([]UserWithConcurrency, 0, len(unique))
	for id, maxConc := range unique {
		batch = append(batch, UserWithConcurrency{
			ID:             id,
			MaxConcurrency: maxConc,
		})
	}

	out := make(map[int64]*UserLoadInfo, len(batch))
	for i := 0; i < len(batch); i += opsConcurrencyBatchChunkSize {
		end := min(i+opsConcurrencyBatchChunkSize, len(batch))
		part, err := s.concurrencyService.GetUsersLoadBatch(ctx, batch[i:end])
		if err != nil {
			// Keep loads from successful chunks. Views apply their zero-activity
			// behavior to missing keys after this failed chunk is omitted.
			logger.LegacyPrintf("service.ops", "[Ops] GetUsersLoadBatch failed: %v", err)
			continue
		}
		maps.Copy(out, part)
	}

	return out
}

// GetUserConcurrencyStats returns active-user observations whose loaded current
// or waiting count is nonzero after monitoring validation. Repository errors
// stop the view, while absent or failed load batches become zero activity and
// can leave an empty successful map.
//
// The response copies observed counters and configured user capacity, computing
// a percentage for positive capacity and leaving zero otherwise. Repeated user
// identities replace their earlier detail row. CollectedAt is sampled between
// identity retrieval and load queries, so it marks assembly timing rather than
// an atomic scheduler snapshot.
func (s *OpsService) GetUserConcurrencyStats(ctx context.Context) (map[int64]*UserConcurrencyInfo, *time.Time, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, nil, err
	}

	users, err := s.listAllActiveUsersForOps(ctx)
	if err != nil {
		return nil, nil, err
	}

	collectedAt := time.Now()
	loadMap := s.getUsersLoadMapBestEffort(ctx, users)

	result := make(map[int64]*UserConcurrencyInfo)

	for _, u := range users {
		if u.ID <= 0 {
			continue
		}

		load := loadMap[u.ID]
		currentInUse := int64(0)
		waiting := int64(0)
		if load != nil {
			currentInUse = int64(load.CurrentConcurrency)
			waiting = int64(load.WaitingCount)
		}

		// Zero activity hides the user row. A missing load entry also becomes zero
		// here, so absence from this view cannot establish that the user was observed
		// idle.
		if currentInUse == 0 && waiting == 0 {
			continue
		}

		info := &UserConcurrencyInfo{
			UserID:         u.ID,
			UserEmail:      u.Email,
			Username:       u.Username,
			CurrentInUse:   currentInUse,
			MaxCapacity:    int64(u.Concurrency),
			WaitingInQueue: waiting,
		}
		if info.MaxCapacity > 0 {
			info.LoadPercentage = float64(info.CurrentInUse) / float64(info.MaxCapacity) * 100
		}
		result[u.ID] = info
	}

	return result, &collectedAt, nil
}
