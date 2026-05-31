package message

import (
	"time"

	"heckel.io/ntfy/v2/db"
)

// MySQL runtime query constants. The placeholder style is "?" (same as SQLite),
// "key" is backtick-quoted because it is a MySQL reserved word, and the
// per-tier expiry/attachment predicates are reapplied here because MySQL has no
// partial-index equivalent (see message/cache_mysql_schema.go).
const (
	mysqlInsertMessageQuery = `
		INSERT INTO message (mid, sequence_id, time, event, expires, topic, message, title, priority, tags, click, icon, actions, attachment_name, attachment_type, attachment_size, attachment_expires, attachment_url, attachment_deleted, sender, user_id, content_type, encoding, published)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	mysqlSelectScheduledMessageIDsBySeqIDQuery = `SELECT mid FROM message WHERE topic = ? AND sequence_id = ? AND published = FALSE`
	mysqlDeleteScheduledBySequenceIDQuery      = `DELETE FROM message WHERE topic = ? AND sequence_id = ? AND published = FALSE`
	mysqlUpdateMessagesForTopicExpiryQuery     = `UPDATE message SET expires = ? WHERE topic = ?`
	mysqlSelectMessagesByIDQuery               = `
		SELECT mid, sequence_id, time, event, expires, topic, message, title, priority, tags, click, icon, actions, attachment_name, attachment_type, attachment_size, attachment_expires, attachment_url, sender, user_id, content_type, encoding
		FROM message
		WHERE mid = ?
	`
	mysqlSelectMessagesSinceTimeQuery = `
		SELECT mid, sequence_id, time, event, expires, topic, message, title, priority, tags, click, icon, actions, attachment_name, attachment_type, attachment_size, attachment_expires, attachment_url, sender, user_id, content_type, encoding
		FROM message
		WHERE topic = ? AND time >= ? AND published = TRUE
		ORDER BY time, id
	`
	mysqlSelectMessagesSinceTimeIncludeScheduledQuery = `
		SELECT mid, sequence_id, time, event, expires, topic, message, title, priority, tags, click, icon, actions, attachment_name, attachment_type, attachment_size, attachment_expires, attachment_url, sender, user_id, content_type, encoding
		FROM message
		WHERE topic = ? AND time >= ?
		ORDER BY time, id
	`
	mysqlSelectMessagesSinceIDQuery = `
		SELECT mid, sequence_id, time, event, expires, topic, message, title, priority, tags, click, icon, actions, attachment_name, attachment_type, attachment_size, attachment_expires, attachment_url, sender, user_id, content_type, encoding
		FROM message
		WHERE topic = ?
		  AND id > COALESCE((SELECT id FROM (SELECT id FROM message WHERE mid = ?) AS m), 0)
		  AND published = TRUE
		ORDER BY time, id
	`
	mysqlSelectMessagesSinceIDIncludeScheduledQuery = `
		SELECT mid, sequence_id, time, event, expires, topic, message, title, priority, tags, click, icon, actions, attachment_name, attachment_type, attachment_size, attachment_expires, attachment_url, sender, user_id, content_type, encoding
		FROM message
		WHERE topic = ?
		  AND (id > COALESCE((SELECT id FROM (SELECT id FROM message WHERE mid = ?) AS m), 0) OR published = FALSE)
		ORDER BY time, id
	`
	mysqlSelectMessagesLatestQuery = `
		SELECT mid, sequence_id, time, event, expires, topic, message, title, priority, tags, click, icon, actions, attachment_name, attachment_type, attachment_size, attachment_expires, attachment_url, sender, user_id, content_type, encoding
		FROM message
		WHERE topic = ? AND published = TRUE
		ORDER BY time DESC, id DESC
		LIMIT 1
	`
	mysqlSelectMessagesDueQuery = `
		SELECT mid, sequence_id, time, event, expires, topic, message, title, priority, tags, click, icon, actions, attachment_name, attachment_type, attachment_size, attachment_expires, attachment_url, sender, user_id, content_type, encoding
		FROM message
		WHERE time <= ? AND published = FALSE
		ORDER BY time, id
	`
	mysqlUpdateMessagePublishedQuery = `UPDATE message SET published = TRUE WHERE mid = ?`
	mysqlSelectMessagesCountQuery    = `SELECT COUNT(*) FROM message`
	mysqlSelectTopicsQuery           = `SELECT topic FROM message GROUP BY topic`

	// DELETE ... LIMIT ... cannot reference a subquery on the same table in
	// MySQL ("You can't specify target table 'message' for update in FROM
	// clause"), but DELETE ... WHERE ... LIMIT N is supported directly.
	mysqlDeleteExpiredMessagesQuery         = `DELETE FROM message WHERE expires <= ? AND published = TRUE LIMIT ?`
	mysqlMarkExpiredAttachmentsDeletedQuery = `UPDATE message SET attachment_deleted = TRUE WHERE attachment_expires > 0 AND attachment_expires <= ? AND attachment_deleted = FALSE LIMIT ?`
	mysqlSelectAttachmentsSizeBySenderQuery = `SELECT COALESCE(SUM(attachment_size), 0) FROM message WHERE user_id = '' AND sender = ? AND attachment_expires >= ?`
	mysqlSelectAttachmentsSizeByUserIDQuery = `SELECT COALESCE(SUM(attachment_size), 0) FROM message WHERE user_id = ? AND attachment_expires >= ?`
	mysqlSelectAttachmentsWithSizesQuery    = `SELECT mid, attachment_size FROM message WHERE attachment_expires > ? AND attachment_deleted = FALSE`

	mysqlSelectStatsQuery       = "SELECT value FROM message_stats WHERE `key` = 'messages'"
	mysqlUpdateStatsQuery       = "UPDATE message_stats SET value = ? WHERE `key` = 'messages'"
	mysqlUpdateMessageTimeQuery = `UPDATE message SET time = ? WHERE mid = ?`
)

var mysqlQueries = queries{
	insertMessage:                    mysqlInsertMessageQuery,
	selectScheduledMessageIDsBySeqID: mysqlSelectScheduledMessageIDsBySeqIDQuery,
	deleteScheduledBySequenceID:      mysqlDeleteScheduledBySequenceIDQuery,
	updateMessagesForTopicExpiry:     mysqlUpdateMessagesForTopicExpiryQuery,
	selectMessagesByID:               mysqlSelectMessagesByIDQuery,
	selectMessagesSinceTime:          mysqlSelectMessagesSinceTimeQuery,
	selectMessagesSinceTimeScheduled: mysqlSelectMessagesSinceTimeIncludeScheduledQuery,
	selectMessagesSinceID:            mysqlSelectMessagesSinceIDQuery,
	selectMessagesSinceIDScheduled:   mysqlSelectMessagesSinceIDIncludeScheduledQuery,
	selectMessagesLatest:             mysqlSelectMessagesLatestQuery,
	selectMessagesDue:                mysqlSelectMessagesDueQuery,
	deleteExpiredMessages:            mysqlDeleteExpiredMessagesQuery,
	updateMessagePublished:           mysqlUpdateMessagePublishedQuery,
	selectMessagesCount:              mysqlSelectMessagesCountQuery,
	selectTopics:                     mysqlSelectTopicsQuery,
	markExpiredAttachmentsDeleted:    mysqlMarkExpiredAttachmentsDeletedQuery,
	selectAttachmentsSizeBySender:    mysqlSelectAttachmentsSizeBySenderQuery,
	selectAttachmentsSizeByUserID:    mysqlSelectAttachmentsSizeByUserIDQuery,
	selectAttachmentsWithSizes:       mysqlSelectAttachmentsWithSizesQuery,
	selectStats:                      mysqlSelectStatsQuery,
	updateStats:                      mysqlUpdateStatsQuery,
	updateMessageTime:                mysqlUpdateMessageTimeQuery,
}

// NewMySQLStore creates a new MySQL-backed message cache store using an existing
// database connection pool. The pool must point at MySQL 8.0.20 or newer (the
// version check is done at db/mysql.Open time, not here).
//
// As with the PostgreSQL store, the mutex passed to newCache is nil because
// MySQL supports concurrent writers — only SQLite needs the single-writer
// serialization that the message cache's mu field provides.
func NewMySQLStore(d *db.DB, batchSize int, batchTimeout time.Duration) (*Cache, error) {
	if err := setupMySQL(d.Primary()); err != nil {
		return nil, err
	}
	return newCache(d, mysqlQueries, nil, batchSize, batchTimeout, false), nil
}
