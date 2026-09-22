package sessioncatalog

import (
	"context"
	"database/sql"
)

const metadataTopicIndex = `CREATE INDEX IF NOT EXISTS idx_catalog_topics_registered_metadata
 ON catalog_topics(scope,workspace_root_key,topic_id) WHERE metadata_present=1`

const registeredMetadataTopics = `SELECT scope,workspace_root,workspace_root_key,topic_id
 FROM catalog_topics INDEXED BY idx_catalog_topics_registered_metadata WHERE metadata_present=1`

// The registry owns only metadata_present rows. Session-only topic removal is
// owned by the source mutation/reconcile transaction; a metadata refresh must
// not rewrite every discovered session merely to update those registry rows.
func (c *Catalog) beginMetadataTopicRefresh(ctx context.Context, tx *sql.Tx) ([]TopicKey, error) {
	if !c.opts.MetadataOnly {
		_, err := tx.ExecContext(ctx, `UPDATE catalog_topics SET metadata_present=0`)
		return nil, err
	}
	// This optional accelerator is built by the background metadata worker.
	// Older writers maintain it automatically through the existing flag; it
	// introduces no new persisted version or authoritative representation.
	if _, err := tx.ExecContext(ctx, metadataTopicIndex); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, registeredMetadataTopics)
	if err != nil {
		return nil, err
	}
	previous := []TopicKey{}
	for rows.Next() {
		var key TopicKey
		if err := rows.Scan(&key.Scope, &key.WorkspaceRoot, &key.workspaceKey, &key.TopicID); err != nil {
			rows.Close()
			return nil, err
		}
		previous = append(previous, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_topics INDEXED BY idx_catalog_topics_registered_metadata
 SET metadata_present=0 WHERE metadata_present=1`)
	return previous, err
}

const orphanMetadataPredicate = `metadata_present=0 AND NOT EXISTS (
 SELECT 1 FROM catalog_sessions s WHERE s.scope=catalog_topics.scope
 AND s.workspace_root_key=catalog_topics.workspace_root_key AND s.topic_id=catalog_topics.topic_id)`

func (c *Catalog) finishMetadataTopicRefresh(ctx context.Context, tx *sql.Tx, previous []TopicKey) error {
	if !c.opts.MetadataOnly {
		_, err := tx.ExecContext(ctx, `DELETE FROM catalog_topics WHERE `+orphanMetadataPredicate)
		return err
	}
	for _, key := range previous {
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_topics
 WHERE scope=? AND workspace_root_key=? AND topic_id=? AND `+orphanMetadataPredicate,
			key.Scope, key.workspaceKey, key.TopicID); err != nil {
			return err
		}
	}
	return nil
}
