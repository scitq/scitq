package server

import (
	"context"
	"sort"
	"time"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ListQuotas reports every provider/region known to the database or to the
// quota manager: the limits the recruiter checks, what scitq's workers
// consume of them, and the provider's quota or capacity refusals of the last
// hour, which reveal limits scitq is not configured with (an Azure VM family
// quota, a stockout).
func (s *taskQueueServer) ListQuotas(ctx context.Context, _ *emptypb.Empty) (*pb.QuotaList, error) {
	byKey := map[string]*pb.RegionQuota{}
	add := func(provider, region string) *pb.RegionQuota {
		key := provider + "/" + region
		if q, ok := byKey[key]; ok {
			return q
		}
		q := &pb.RegionQuota{Provider: provider, Region: region}
		byKey[key] = q
		return q
	}

	for _, st := range s.qm.Snapshot() {
		q := add(st.Provider, st.Region)
		q.Configured = st.Configured
		q.MaxCpu = st.MaxCPU
		q.MaxMemGb = st.MaxMemGB
		q.MaxInstances = st.MaxInstances
		q.InstancesLearned = st.Learned
		q.UsedCpu = st.Usage.UsedCPU
		q.UsedMemGb = st.Usage.UsedMemGB
		q.UsedInstances = st.Usage.Instances
		if !st.CooldownUntil.IsZero() {
			c := st.CooldownUntil.UTC().Format(time.RFC3339)
			q.CooldownUntil = &c
		}
	}

	rows, err := s.db.QueryContext(ctx, `
		WITH refusal AS (
		  SELECT region_id, error_class, error_message, modified_at,
		         COUNT(*) OVER (PARTITION BY region_id) AS n,
		         ROW_NUMBER() OVER (PARTITION BY region_id ORDER BY modified_at DESC) AS rk
		  FROM job
		  WHERE action = 'C' AND error_class IN ('quota', 'capacity')
		    AND modified_at > NOW() - INTERVAL '1 hour'
		)
		SELECT p.provider_name || '.' || p.config_name, rg.region_name,
		       COALESCE(r.n, 0), r.error_class, r.error_message, r.modified_at
		FROM region rg
		JOIN provider p ON p.provider_id = rg.provider_id
		LEFT JOIN refusal r ON r.region_id = rg.region_id AND r.rk = 1
		`)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list quotas: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var provider, region string
		var n int32
		var class, msg, at *string
		if err := rows.Scan(&provider, &region, &n, &class, &msg, &at); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to scan quota row: %v", err)
		}
		q := add(provider, region)
		q.RecentRefusals = n
		q.LastRefusalClass = class
		q.LastRefusalMessage = msg
		q.LastRefusalAt = at
	}
	if err := rows.Err(); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list quotas: %v", err)
	}

	out := &pb.QuotaList{Quotas: make([]*pb.RegionQuota, 0, len(byKey))}
	for _, q := range byKey {
		q.State = quotaState(q)
		out.Quotas = append(out.Quotas, q)
	}
	sort.Slice(out.Quotas, func(i, j int) bool {
		a, b := out.Quotas[i], out.Quotas[j]
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		return a.Region < b.Region
	})
	return out, nil
}

// quotaState tells whether a region can take more workers; the first
// matching reason wins.
func quotaState(q *pb.RegionQuota) string {
	switch {
	case !q.Configured:
		return "unconfigured"
	case q.CooldownUntil != nil:
		return "cooldown"
	case q.UsedCpu >= q.MaxCpu,
		q.MaxMemGb > 0 && q.UsedMemGb >= q.MaxMemGb,
		q.MaxInstances > 0 && q.UsedInstances >= q.MaxInstances:
		return "full"
	case q.RecentRefusals > 0:
		return "refusing"
	default:
		return "available"
	}
}
