package bench

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/batchcorp/njst/cli"
	"github.com/batchcorp/njst/natssvc"
	"github.com/batchcorp/njst/types"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	DefaultNumStreams            = 1
	DefaultBatchSize             = 100
	DefaultMsgSizeBytes          = 1024
	DefaultNumMessagesPerStream  = 10000
	DefaultNumWorkersPerStream   = 1
	DefaultReadStrategy          = types.SpreadReadStrategy
	DefaultConsumerGroupStrategy = types.PerJobConsumerGroupStrategy
)

var (
	ValidReadStrategies = map[types.ReadStrategy]struct{}{
		types.SpreadReadStrategy: {},
		types.SharedReadStrategy: {},
	}

	ValidConsumerGroupStrategies = map[types.ConsumerGroupStrategy]struct{}{
		types.PerJobConsumerGroupStrategy:    {},
		types.PerStreamConsumerGroupStrategy: {},
		types.NoneConsumerGroupStrategy:      {},
	}
)

type Bench struct {
	nats          *natssvc.NATSService
	params        *cli.Params
	producerMap   map[string]*Producer
	consumerMap   map[string]*Consumer
	producerMutex *sync.RWMutex
	consumerMutex *sync.RWMutex
	log           *logrus.Entry
}

type Producer struct {
	Settings *types.WriteSettings
}

type Consumer struct {
	Settings *types.ReadSettings
}

func New(p *cli.Params, nsvc *natssvc.NATSService) (*Bench, error) {
	if err := validateParams(p); err != nil {
		return nil, errors.Wrap(err, "unable to validate params")
	}

	if nsvc == nil {
		return nil, errors.New("nats service cannot be nil")
	}

	return &Bench{
		params:        p,
		nats:          nsvc,
		producerMap:   make(map[string]*Producer),
		consumerMap:   make(map[string]*Consumer),
		producerMutex: &sync.RWMutex{},
		consumerMutex: &sync.RWMutex{},
		log:           logrus.WithField("pkg", "bench"),
	}, nil
}

func (b *Bench) Delete(jobID string) error {
	// Create delete jobs
	deleteJobs, err := b.GenerateDeleteJobs(jobID)
	if err != nil {
		return errors.Wrap(err, "unable to create delete jobs")
	}

	// Emit delete jobs
	if err := b.nats.EmitJobs(types.DeleteJob, deleteJobs); err != nil {
		return errors.Wrap(err, "unable to emit delete jobs")
	}

	// Delete settings
	if err := b.nats.DeleteSettings(jobID); err != nil {
		return errors.Wrap(err, "unable to delete settings")
	}

	// Delete results
	if err := b.nats.DeleteResults(jobID); err != nil {
		return errors.Wrap(err, "unable to delete results")
	}

	return nil
}

func (b *Bench) Status(id string) (*types.Status, error) {
	fullBucketName := fmt.Sprintf("%s-%s", natssvc.ResultBucketPrefix, id)

	bucket, err := b.nats.GetBucket(fullBucketName)
	if err != nil {
		return nil, errors.Wrap(err, "unable to get bucket")
	}

	keys, err := bucket.Keys()
	if err != nil {
		return nil, errors.Wrap(err, "unable to get keys")
	}

	finalStatus := &types.Status{}

	for _, key := range keys {
		entry, err := bucket.Get(key)
		if err != nil {
			return nil, errors.Wrap(err, "unable to get k/v entry")
		}

		s := &types.Status{}

		if err := json.Unmarshal(entry.Value(), s); err != nil {
			return nil, errors.Wrap(err, "unable to unmarshal status")
		}

		finalStatus.JobID = s.JobID
		finalStatus.NodeID = s.NodeID
		finalStatus.Message = s.Message
		finalStatus.TotalProcessed = finalStatus.TotalProcessed + s.TotalProcessed
		finalStatus.TotalErrors = finalStatus.TotalErrors + s.TotalErrors

		finalStatus.Status = s.Status

		if len(s.Errors) != 0 {
			finalStatus.Errors = append(finalStatus.Errors, s.Errors...)
		}

		if s.ElapsedSeconds > finalStatus.ElapsedSeconds {
			finalStatus.ElapsedSeconds = s.ElapsedSeconds
		}

		if finalStatus.StartedAt.IsZero() {
			finalStatus.StartedAt = s.StartedAt
		}

		// Want to have the earliest start time
		if s.StartedAt.Before(finalStatus.StartedAt) {
			finalStatus.StartedAt = s.StartedAt
		}

		// Want to have the latest end time
		if s.EndedAt.After(finalStatus.EndedAt) {
			finalStatus.EndedAt = s.EndedAt
		}

		avgMsgPerSec := finalStatus.TotalProcessed / finalStatus.ElapsedSeconds

		if finalStatus.AvgMsgPerSec == 0 {
			finalStatus.AvgMsgPerSec = avgMsgPerSec
		} else {
			finalStatus.AvgMsgPerSec = (finalStatus.AvgMsgPerSec + avgMsgPerSec) / 2
		}
	}

	return finalStatus, nil
}

func (b *Bench) createProducer(settings *types.Settings) (string, error) {
	if err := validateProducerSettings(settings); err != nil {
		return "", errors.Wrap(err, "unable to validate producer settings")
	}

	return "", nil
}

func (b *Bench) createConsumer(settings *types.Settings) (string, error) {
	if err := validateConsumerSettings(settings); err != nil {
		return "", errors.Wrap(err, "unable to validate consumer settings")
	}

	return "", nil
}

func (b *Bench) createReadJobs(settings *types.Settings) ([]*types.Job, error) {
	// TODO: Do the streams exist?
	// TODO: Create consumers
	// TODO: Create job entries

	//for _, streamName := range streams {
	//	consumerGroupName := "cg-" + streamName
	//
	//	if _, err := n.js.AddConsumer(streamName, &nats.ConsumerConfig{
	//		Durable:     consumerGroupName,
	//		Description: "njst consumer",
	//	}); err != nil {
	//		return nil, errors.Wrapf(err, "unable to create consumer for stream '%s': %s", streamName, err)
	//	}
	//
	//	streams[streamName] = consumerGroupName
	//}
	return nil, nil
}

func (b *Bench) createWriteJobs(settings *types.Settings) ([]*types.Job, error) {
	if settings == nil || settings.Write == nil {
		return nil, errors.New("unable to setup write bench without write settings")
	}

	nodes, err := b.nats.GetNodeList()
	if err != nil {
		return nil, errors.Wrap(err, "unable to get node list")
	}

	if settings.Write.NumNodes > len(nodes) {
		return nil, errors.Errorf("unable to create write jobs: %d nodes requested but only %d available", settings.Write.NumNodes, len(nodes))
	}

	streamPrefix := fmt.Sprintf("njst-%s", settings.ID)

	// Create streams
	for i := 0; i < settings.Write.NumStreams; i++ {
		streamName := fmt.Sprintf("%s-%d", streamPrefix, i)

		if _, err := b.nats.AddStream(&nats.StreamConfig{
			Name:        streamName,
			Description: "njst bench stream",
			Subjects:    []string{streamName},
			Storage:     nats.MemoryStorage,
			Replicas:    settings.Write.NumReplicas,
		}); err != nil {
			return nil, errors.Wrapf(err, "unable to create stream '%s'", streamName)
		}
	}

	jobs := make([]*types.Job, 0)

	// How many nodes will this test run on?
	var numSelectedNodes int

	if settings.Write.NumNodes == 0 {
		numSelectedNodes = len(nodes)
	} else {
		numSelectedNodes = settings.Write.NumNodes
	}

	if settings.Write.NumStreams < numSelectedNodes {
		numSelectedNodes = settings.Write.NumStreams
	}

	streamsPerNode := settings.Write.NumStreams / numSelectedNodes
	streamsPerLastNode := streamsPerNode + (settings.Write.NumStreams % numSelectedNodes)

	var startIndex int

	for i := 0; i < numSelectedNodes; i++ {
		numStreams := streamsPerNode

		// If this the last node, add remainder streams (if any)
		if i == numSelectedNodes-1 {
			numStreams = streamsPerLastNode
		}

		jobs = append(jobs, &types.Job{
			NodeID: nodes[i],
			Settings: &types.Settings{
				ID:          settings.ID,
				Description: settings.Description,
				Write: &types.WriteSettings{
					NumMessagesPerStream: settings.Write.NumMessagesPerStream,
					NumWorkersPerStream:  settings.Write.NumWorkersPerStream,
					MsgSizeBytes:         settings.Write.MsgSizeBytes,
					KeepStreams:          settings.Write.KeepStreams,
					Subjects:             generateSubjects(startIndex, numStreams, streamPrefix),
				},
			},
			CreatedBy: b.params.NodeID,
			CreatedAt: time.Now().UTC(),
		})

		startIndex = startIndex + streamsPerNode
	}

	return jobs, nil
}

func generateSubjects(startIndex int, numSubjects int, subjectPrefix string) []string {
	subjects := make([]string, 0)

	for i := startIndex; i != numSubjects+startIndex; i++ {
		subjects = append(subjects, fmt.Sprintf("%s-%d", subjectPrefix, i))
	}

	return subjects
}

func (b *Bench) GenerateCreateJobs(settings *types.Settings) ([]*types.Job, error) {
	if settings == nil {
		return nil, errors.New("settings cannot be nil")
	}

	var err error
	var jobs []*types.Job

	if settings.Read != nil {
		jobs, err = b.createReadJobs(settings)
	} else if settings.Write != nil {
		jobs, err = b.createWriteJobs(settings)
	} else {
		return nil, errors.New("settings must have either read or write set")
	}

	if err != nil {
		return nil, errors.Wrap(err, "unable to create jobs")
	}

	return jobs, nil
}

func (b *Bench) GenerateDeleteJobs(id string) ([]*types.Job, error) {
	nodes, err := b.nats.GetNodeList()
	if err != nil {
		return nil, errors.Wrap(err, "unable to get node list")
	}

	jobs := make([]*types.Job, 0)

	for _, node := range nodes {
		jobs = append(jobs, &types.Job{
			NodeID: node,
			Settings: &types.Settings{
				ID: id,
			},
			CreatedBy: b.params.NodeID,
			CreatedAt: time.Now().UTC(),
		})
	}

	return jobs, nil
}

func (b *Bench) deleteProducer(id string) error {
	return nil
}

func (b *Bench) deleteConsumer(id string) error {
	return nil
}

func (b *Bench) Exists(name string) (bool, error) {
	return false, nil
}

func validateParams(p *cli.Params) error {
	if p == nil {
		return errors.New("params cannot be nil")
	}

	if p.NodeID == "" {
		return errors.New("node id cannot be empty")
	}

	if len(p.NATSAddress) == 0 {
		return errors.New("nats address cannot be empty")
	}

	return nil
}

func validateConsumerSettings(settings *types.Settings) error {
	if settings == nil {
		return errors.New("settings cannot be nil")
	}

	if settings.Read == nil {
		return errors.New("consumer settings cannot be nil")
	}

	return nil
}

func validateProducerSettings(settings *types.Settings) error {
	if settings == nil {
		return errors.New("settings cannot be nil")
	}

	if settings.Write == nil {
		return errors.New("producer settings cannot be nil")
	}

	return nil
}

func GenRandomBytes(size int) ([]byte, error) {
	data := make([]byte, size)

	if _, err := rand.Read(data); err != nil {
		return nil, err
	}

	return data, nil
}
