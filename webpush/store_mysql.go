package webpush

import (
	"heckel.io/ntfy/v2/db"
)

// MySQL-specific SQL queries for the web push store. These mirror the
// PostgreSQL queries in store_postgres.go with the following dialect changes:
//
//   - Parameter placeholders use ? (same as SQLite)
//   - The upsert uses MySQL 8.0.20+ row-alias syntax (AS new ON DUPLICATE KEY
//     UPDATE col = new.col) instead of ON CONFLICT … DO UPDATE … excluded.col
//   - There is no RETURNING clause; the canonical id is fetched in a separate
//     SELECT inside the same transaction (see Store.UpsertSubscription)
//   - The DELETE-without-subscription query wraps its inner SELECT in a
//     derived table to side-step MySQL's "can't specify target table in FROM"
//     restriction
const (
	mysqlSelectSubscriptionIDByEndpointQuery        = "SELECT id FROM webpush_subscription WHERE endpoint = ?"
	mysqlSelectSubscriptionCountBySubscriberIPQuery = "SELECT COUNT(*) FROM webpush_subscription WHERE subscriber_ip = ?"
	mysqlSelectSubscriptionsForTopicQuery           = `
		SELECT s.id, s.endpoint, s.key_auth, s.key_p256dh, s.user_id
		FROM webpush_subscription_topic st
		JOIN webpush_subscription s ON s.id = st.subscription_id
		WHERE st.topic = ?
		ORDER BY s.endpoint
	`
	mysqlSelectSubscriptionsExpiringSoonQuery = `
		SELECT id, endpoint, key_auth, key_p256dh, user_id
		FROM webpush_subscription
		WHERE warned_at = 0 AND updated_at <= ?
	`
	mysqlUpsertSubscriptionQuery = `
		INSERT INTO webpush_subscription (id, endpoint, key_auth, key_p256dh, user_id, subscriber_ip, updated_at, warned_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) AS new
		ON DUPLICATE KEY UPDATE
			key_auth = new.key_auth,
			key_p256dh = new.key_p256dh,
			user_id = new.user_id,
			subscriber_ip = new.subscriber_ip,
			updated_at = new.updated_at,
			warned_at = new.warned_at
	`
	mysqlUpdateSubscriptionWarningSentQuery = "UPDATE webpush_subscription SET warned_at = ? WHERE id = ?"
	mysqlUpdateSubscriptionUpdatedAtQuery   = "UPDATE webpush_subscription SET updated_at = ? WHERE endpoint = ?"
	mysqlDeleteSubscriptionByEndpointQuery  = "DELETE FROM webpush_subscription WHERE endpoint = ?"
	mysqlDeleteSubscriptionByUserIDQuery    = "DELETE FROM webpush_subscription WHERE user_id = ?"
	mysqlDeleteSubscriptionByAgeQuery       = "DELETE FROM webpush_subscription WHERE updated_at <= ?"

	mysqlInsertSubscriptionTopicQuery    = "INSERT INTO webpush_subscription_topic (subscription_id, topic) VALUES (?, ?)"
	mysqlDeleteSubscriptionTopicAllQuery = "DELETE FROM webpush_subscription_topic WHERE subscription_id = ?"
	// Wrap the inner SELECT in a derived table because MySQL otherwise rejects
	// the query with error 1093 ("can't specify target table for update in
	// FROM clause") when the inner SELECT references a table the outer
	// statement is modifying — but in this case the inner SELECT references
	// webpush_subscription, not the deleted-from webpush_subscription_topic,
	// so the bare form works. Kept identical to the postgres query for
	// clarity.
	mysqlDeleteSubscriptionTopicWithoutSubscriptionQuery = "DELETE FROM webpush_subscription_topic WHERE subscription_id NOT IN (SELECT id FROM webpush_subscription)"
)

// NewMySQLStore creates a new MySQL-backed web push store using an existing
// database connection pool.
func NewMySQLStore(d *db.DB) (*Store, error) {
	if err := setupMySQL(d.Primary()); err != nil {
		return nil, err
	}
	return &Store{
		db: d,
		queries: queries{
			selectSubscriptionIDByEndpoint:             mysqlSelectSubscriptionIDByEndpointQuery,
			selectSubscriptionCountBySubscriberIP:      mysqlSelectSubscriptionCountBySubscriberIPQuery,
			selectSubscriptionsForTopic:                mysqlSelectSubscriptionsForTopicQuery,
			selectSubscriptionsExpiringSoon:            mysqlSelectSubscriptionsExpiringSoonQuery,
			upsertSubscription:                         mysqlUpsertSubscriptionQuery,
			updateSubscriptionWarningSent:              mysqlUpdateSubscriptionWarningSentQuery,
			updateSubscriptionUpdatedAt:                mysqlUpdateSubscriptionUpdatedAtQuery,
			deleteSubscriptionByEndpoint:               mysqlDeleteSubscriptionByEndpointQuery,
			deleteSubscriptionByUserID:                 mysqlDeleteSubscriptionByUserIDQuery,
			deleteSubscriptionByAge:                    mysqlDeleteSubscriptionByAgeQuery,
			insertSubscriptionTopic:                    mysqlInsertSubscriptionTopicQuery,
			deleteSubscriptionTopicAll:                 mysqlDeleteSubscriptionTopicAllQuery,
			deleteSubscriptionTopicWithoutSubscription: mysqlDeleteSubscriptionTopicWithoutSubscriptionQuery,
			upsertReturnsID:                            false,
		},
	}, nil
}
