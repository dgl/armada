package armada

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/alicebob/miniredis"
	"github.com/go-redis/redis"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/G-Research/armada/internal/armada/authorization/permissions"
	"github.com/G-Research/armada/internal/armada/configuration"
	"github.com/G-Research/armada/internal/common"
	"github.com/G-Research/armada/pkg/api"
)

func TestSubmitJob_EmptyPodSpec(t *testing.T) {
	withRunningServer(func(client api.SubmitClient, leaseClient api.AggregatedQueueClient, ctx context.Context) {
		_, err := client.CreateQueue(ctx, &api.Queue{
			Name:           "test",
			PriorityFactor: 1,
		})
		assert.Empty(t, err)

		request := &api.JobSubmitRequest{
			JobRequestItems: []*api.JobSubmitRequestItem{{}},
			Queue:           "test",
			JobSetId:        "set",
		}
		_, err = client.SubmitJobs(ctx, request)
		assert.Error(t, err)
	})
}

func TestSubmitJob(t *testing.T) {
	withRunningServer(func(client api.SubmitClient, leaseClient api.AggregatedQueueClient, ctx context.Context) {

		_, err := client.CreateQueue(ctx, &api.Queue{
			Name:           "test",
			PriorityFactor: 1,
		})
		assert.Empty(t, err)

		cpu, _ := resource.ParseQuantity("1")
		memory, _ := resource.ParseQuantity("512Mi")

		jobId := SubmitJob(client, ctx, cpu, memory, t)

		leasedResponse, err := leaseClient.LeaseJobs(ctx, &api.LeaseRequest{
			ClusterId: "test-cluster",
			Resources: common.ComputeResources{"cpu": cpu, "memory": memory},
		})
		assert.Empty(t, err)

		assert.Equal(t, 1, len(leasedResponse.Job))
		assert.Equal(t, jobId, leasedResponse.Job[0].Id)
	})
}

func TestCancelJob(t *testing.T) {
	withRunningServer(func(client api.SubmitClient, leaseClient api.AggregatedQueueClient, ctx context.Context) {

		_, err := client.CreateQueue(ctx, &api.Queue{
			Name:           "test",
			PriorityFactor: 1,
		})
		assert.Empty(t, err)

		cpu, _ := resource.ParseQuantity("1")
		memory, _ := resource.ParseQuantity("512Mi")

		SubmitJob(client, ctx, cpu, memory, t)
		SubmitJob(client, ctx, cpu, memory, t)

		leasedResponse, err := leaseClient.LeaseJobs(ctx, &api.LeaseRequest{
			ClusterId: "test-cluster",
			Resources: common.ComputeResources{"cpu": cpu, "memory": memory},
		})
		assert.Empty(t, err)
		assert.Equal(t, 1, len(leasedResponse.Job))

		cancelResult, err := client.CancelJobs(ctx, &api.JobCancelRequest{JobSetId: "set", Queue: "test"})
		assert.Empty(t, err)
		assert.Equal(t, 2, len(cancelResult.CancelledIds))

		renewed, err := leaseClient.RenewLease(ctx, &api.RenewLeaseRequest{
			ClusterId: "test-cluster",
			Ids:       []string{leasedResponse.Job[0].Id},
		})
		assert.Empty(t, err)
		assert.Equal(t, 0, len(renewed.Ids))

	})
}

func SubmitJob(client api.SubmitClient, ctx context.Context, cpu resource.Quantity, memory resource.Quantity, t *testing.T) string {
	request := &api.JobSubmitRequest{
		JobRequestItems: []*api.JobSubmitRequestItem{
			{
				PodSpec: &v1.PodSpec{
					Containers: []v1.Container{{
						Name:  "Container1",
						Image: "index.docker.io/library/ubuntu:latest",
						Args:  []string{"sleep", "10s"},
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{"cpu": cpu, "memory": memory},
							Limits:   v1.ResourceList{"cpu": cpu, "memory": memory},
						},
					},
					},
				},
				Priority: 0,
			},
		},
		Queue:    "test",
		JobSetId: "set",
	}
	response, err := client.SubmitJobs(ctx, request)
	assert.Empty(t, err)
	return response.JobResponseItems[0].JobId
}

func withRunningServer(action func(client api.SubmitClient, leaseClient api.AggregatedQueueClient, ctx context.Context)) {
	minidb, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	defer minidb.Close()

	// cleanup prometheus in case there are registered metrics already present
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
	shutdown, _ := Serve(&configuration.ArmadaConfig{
		AnonymousAuth: true,
		GrpcPort:      50052,
		Redis: redis.UniversalOptions{
			Addrs: []string{minidb.Addr()},
			DB:    0,
		},
		PermissionGroupMapping: map[permissions.Permission][]string{
			permissions.ExecuteJobs:    {"everyone"},
			permissions.SubmitJobs:     {"everyone"},
			permissions.SubmitAnyJobs:  {"everyone"},
			permissions.CreateQueue:    {"everyone"},
			permissions.CancelJobs:     {"everyone"},
			permissions.CancelAnyJobs:  {"everyone"},
			permissions.WatchAllEvents: {"everyone"},
		},
		Scheduling: configuration.SchedulingConfig{
			QueueLeaseBatchSize: 100,
		},
	})
	defer shutdown()

	conn, err := grpc.Dial("localhost:50052", grpc.WithInsecure(), grpc.WithDefaultCallOptions(grpc.WaitForReady(true)))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	setupServer(conn)
	client := api.NewSubmitClient(conn)
	leaseClient := api.NewAggregatedQueueClient(conn)
	ctx := context.Background()

	action(client, leaseClient, ctx)
}

func setupServer(conn *grpc.ClientConn) {
	ctx := context.Background()
	usageReport := &api.ClusterUsageReport{
		ClusterId:                "test-cluster",
		ReportTime:               time.Now(),
		Queues:                   []*api.QueueReport{},
		ClusterCapacity:          map[string]resource.Quantity{"cpu": resource.MustParse("100"), "memory": resource.MustParse("100Gi")},
		ClusterAvailableCapacity: map[string]resource.Quantity{"cpu": resource.MustParse("100"), "memory": resource.MustParse("100Gi")},
	}
	usageClient := api.NewUsageClient(conn)
	_, _ = usageClient.ReportUsage(ctx, usageReport)
}
