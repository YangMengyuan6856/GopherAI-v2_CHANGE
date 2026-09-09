package incident

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"GopherAI/model"

	rediscli "github.com/redis/go-redis/v9"
)

type CaseIndexer interface {
	Index(context.Context, model.ResolvedIncident) error
}

type RedisCaseIndexer struct {
	client *rediscli.Client
	prefix string
}

func NewRedisCaseIndexer(client *rediscli.Client, environment string) (*RedisCaseIndexer, error) {
	environment = strings.TrimSpace(strings.ToLower(environment))
	if client == nil || environment == "" {
		return nil, errors.New("redis client and environment are required")
	}
	return &RedisCaseIndexer{client: client, prefix: "gopherai:" + environment + ":incident:v1"}, nil
}

func (indexer *RedisCaseIndexer) Index(ctx context.Context, incident model.ResolvedIncident) error {
	if indexer == nil || indexer.client == nil || incident.ID == "" || incident.TenantIDHash == "" || incident.UserIDHash == "" || incident.Status != StatusConfirmed {
		return errors.New("confirmed incident identity is required")
	}
	key := indexer.caseKey(incident.UserIDHash, incident.ID)
	values := map[string]any{
		"incident_id": incident.ID, "tenant_id_hash": incident.TenantIDHash, "user_id_hash": incident.UserIDHash,
		"version": incident.Version, "symptom": incident.Symptom, "root_cause": incident.RootCause,
		"resolution": incident.Resolution, "components": incident.ComponentsJSON,
		"error_signatures": incident.ErrorSignaturesJSON, "confirmed_at": incident.ConfirmedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	var signatures, components []string
	if err := json.Unmarshal([]byte(incident.ErrorSignaturesJSON), &signatures); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(incident.ComponentsJSON), &components); err != nil {
		return err
	}
	_, err := indexer.client.TxPipelined(ctx, func(pipe rediscli.Pipeliner) error {
		pipe.HSet(ctx, key, values)
		pipe.SAdd(ctx, indexer.ownerKey(incident.UserIDHash), incident.ID)
		for _, signature := range signatures {
			pipe.SAdd(ctx, indexer.signalKey(incident.UserIDHash, "signature", signature), incident.ID)
		}
		for _, component := range components {
			pipe.SAdd(ctx, indexer.signalKey(incident.UserIDHash, "component", component), incident.ID)
		}
		return nil
	})
	return err
}

func (indexer *RedisCaseIndexer) caseKey(userIDHash string, incidentID string) string {
	return fmt.Sprintf("%s:case:%s:%s", indexer.prefix, userIDHash, incidentID)
}

func (indexer *RedisCaseIndexer) ownerKey(userIDHash string) string {
	return fmt.Sprintf("%s:owner:%s", indexer.prefix, userIDHash)
}

func (indexer *RedisCaseIndexer) signalKey(userIDHash string, kind string, value string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(value))))
	return fmt.Sprintf("%s:%s:%s:%s", indexer.prefix, kind, userIDHash, hex.EncodeToString(sum[:]))
}
