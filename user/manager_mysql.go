package user

import (
	"heckel.io/ntfy/v2/db"
)

// MySQL queries. The translation rules from manager_postgres.go are:
//
//   - placeholders $1, $2, ...  ->  ?  (same as SQLite, queries are otherwise
//     the same shape as postgres)
//   - "user" reserved word     ->  `user` (backticks)
//   - "read", "write" columns  ->  `read`, `write` (backticks) — reserved in
//     MySQL 8.0
//   - ON CONFLICT (cols) DO UPDATE SET c = excluded.c
//                                 ->  AS new ON DUPLICATE KEY UPDATE c = new.c
//                                     (row-alias syntax requires MySQL 8.0.20+)
//   - LIKE ... ESCAPE '\'      ->  LIKE ... ESCAPE '\\'  (MySQL string literal
//                                     processes backslashes, so '\\' is the
//                                     1-character backslash escape)
const (
	// User queries
	mysqlSelectUsersQuery = "" +
		"SELECT u.id, u.user_name, u.pass, u.role, u.prefs, u.sync_topic, u.provisioned, u.stats_messages, u.stats_emails, u.stats_calls, u.stripe_customer_id, u.stripe_subscription_id, u.stripe_subscription_status, u.stripe_subscription_interval, u.stripe_subscription_paid_until, u.stripe_subscription_cancel_at, u.deleted, t.id, t.code, t.name, t.messages_limit, t.messages_expiry_duration, t.emails_limit, t.calls_limit, t.reservations_limit, t.attachment_file_size_limit, t.attachment_total_size_limit, t.attachment_expiry_duration, t.attachment_bandwidth_limit, t.stripe_monthly_price_id, t.stripe_yearly_price_id " +
		"FROM `user` u " +
		"LEFT JOIN tier t on t.id = u.tier_id " +
		"ORDER BY CASE u.role WHEN 'admin' THEN 1 WHEN 'anonymous' THEN 3 ELSE 2 END, u.user_name"
	mysqlSelectUserByIDQuery = "" +
		"SELECT u.id, u.user_name, u.pass, u.role, u.prefs, u.sync_topic, u.provisioned, u.stats_messages, u.stats_emails, u.stats_calls, u.stripe_customer_id, u.stripe_subscription_id, u.stripe_subscription_status, u.stripe_subscription_interval, u.stripe_subscription_paid_until, u.stripe_subscription_cancel_at, u.deleted, t.id, t.code, t.name, t.messages_limit, t.messages_expiry_duration, t.emails_limit, t.calls_limit, t.reservations_limit, t.attachment_file_size_limit, t.attachment_total_size_limit, t.attachment_expiry_duration, t.attachment_bandwidth_limit, t.stripe_monthly_price_id, t.stripe_yearly_price_id " +
		"FROM `user` u " +
		"LEFT JOIN tier t on t.id = u.tier_id " +
		"WHERE u.id = ?"
	mysqlSelectUserByNameQuery = "" +
		"SELECT u.id, u.user_name, u.pass, u.role, u.prefs, u.sync_topic, u.provisioned, u.stats_messages, u.stats_emails, u.stats_calls, u.stripe_customer_id, u.stripe_subscription_id, u.stripe_subscription_status, u.stripe_subscription_interval, u.stripe_subscription_paid_until, u.stripe_subscription_cancel_at, u.deleted, t.id, t.code, t.name, t.messages_limit, t.messages_expiry_duration, t.emails_limit, t.calls_limit, t.reservations_limit, t.attachment_file_size_limit, t.attachment_total_size_limit, t.attachment_expiry_duration, t.attachment_bandwidth_limit, t.stripe_monthly_price_id, t.stripe_yearly_price_id " +
		"FROM `user` u " +
		"LEFT JOIN tier t on t.id = u.tier_id " +
		"WHERE u.user_name = ?"
	mysqlSelectUserByTokenQuery = "" +
		"SELECT u.id, u.user_name, u.pass, u.role, u.prefs, u.sync_topic, u.provisioned, u.stats_messages, u.stats_emails, u.stats_calls, u.stripe_customer_id, u.stripe_subscription_id, u.stripe_subscription_status, u.stripe_subscription_interval, u.stripe_subscription_paid_until, u.stripe_subscription_cancel_at, u.deleted, t.id, t.code, t.name, t.messages_limit, t.messages_expiry_duration, t.emails_limit, t.calls_limit, t.reservations_limit, t.attachment_file_size_limit, t.attachment_total_size_limit, t.attachment_expiry_duration, t.attachment_bandwidth_limit, t.stripe_monthly_price_id, t.stripe_yearly_price_id " +
		"FROM `user` u " +
		"JOIN user_token tk on u.id = tk.user_id " +
		"LEFT JOIN tier t on t.id = u.tier_id " +
		"WHERE tk.token = ? AND (tk.expires = 0 OR tk.expires >= ?)"
	mysqlSelectUserByStripeCustomerIDQuery = "" +
		"SELECT u.id, u.user_name, u.pass, u.role, u.prefs, u.sync_topic, u.provisioned, u.stats_messages, u.stats_emails, u.stats_calls, u.stripe_customer_id, u.stripe_subscription_id, u.stripe_subscription_status, u.stripe_subscription_interval, u.stripe_subscription_paid_until, u.stripe_subscription_cancel_at, u.deleted, t.id, t.code, t.name, t.messages_limit, t.messages_expiry_duration, t.emails_limit, t.calls_limit, t.reservations_limit, t.attachment_file_size_limit, t.attachment_total_size_limit, t.attachment_expiry_duration, t.attachment_bandwidth_limit, t.stripe_monthly_price_id, t.stripe_yearly_price_id " +
		"FROM `user` u " +
		"LEFT JOIN tier t on t.id = u.tier_id " +
		"WHERE u.stripe_customer_id = ?"
	mysqlSelectUsernamesQuery = "" +
		"SELECT user_name FROM `user` " +
		"ORDER BY CASE role WHEN 'admin' THEN 1 WHEN 'anonymous' THEN 3 ELSE 2 END, user_name"
	mysqlSelectUserCountQuery          = "SELECT COUNT(*) FROM `user`"
	mysqlSelectUserIDFromUsernameQuery = "SELECT id FROM `user` WHERE user_name = ?"
	mysqlInsertUserQuery               = "INSERT INTO `user` (id, user_name, pass, role, prefs, sync_topic, provisioned, created) VALUES (?, ?, ?, ?, '{}', ?, ?, ?)"
	mysqlUpdateUserPassQuery           = "UPDATE `user` SET pass = ? WHERE user_name = ?"
	mysqlUpdateUserRoleQuery           = "UPDATE `user` SET role = ? WHERE user_name = ?"
	mysqlUpdateUserProvisionedQuery    = "UPDATE `user` SET provisioned = ? WHERE user_name = ?"
	mysqlUpdateUserPrefsQuery          = "UPDATE `user` SET prefs = ? WHERE id = ?"
	mysqlUpdateUserStatsQuery          = "UPDATE `user` SET stats_messages = ?, stats_emails = ?, stats_calls = ? WHERE id = ?"
	mysqlUpdateUserStatsResetAllQuery  = "UPDATE `user` SET stats_messages = 0, stats_emails = 0, stats_calls = 0"
	mysqlUpdateUserTierQuery           = "UPDATE `user` SET tier_id = (SELECT id FROM tier WHERE code = ?) WHERE user_name = ?"
	mysqlUpdateUserDeletedQuery        = "UPDATE `user` SET deleted = ? WHERE id = ?"
	mysqlDeleteUserQuery               = "DELETE FROM `user` WHERE user_name = ?"
	mysqlDeleteUserTierQuery           = "UPDATE `user` SET tier_id = NULL WHERE user_name = ?"
	mysqlDeleteUsersMarkedQuery        = "DELETE FROM `user` WHERE deleted < ?"
	mysqlDeleteUsersProvisionedQuery   = "DELETE FROM `user` WHERE provisioned = TRUE"

	// Access queries
	mysqlSelectTopicPermsQuery = "" +
		"SELECT `read`, `write` " +
		"FROM user_access a " +
		"JOIN `user` u ON u.id = a.user_id " +
		"WHERE (u.user_name = ? OR u.user_name = ?) AND ? LIKE a.topic ESCAPE '\\\\' " +
		"ORDER BY u.user_name DESC, LENGTH(a.topic) DESC, CASE WHEN a.`write` THEN 1 ELSE 0 END DESC"
	mysqlSelectUserAllAccessQuery = "" +
		"SELECT user_id, topic, `read`, `write`, provisioned " +
		"FROM user_access " +
		"ORDER BY LENGTH(topic) DESC, CASE WHEN `write` THEN 1 ELSE 0 END DESC, CASE WHEN `read` THEN 1 ELSE 0 END DESC, topic"
	mysqlSelectUserAccessQuery = "" +
		"SELECT topic, `read`, `write`, provisioned " +
		"FROM user_access " +
		"WHERE user_id = (SELECT id FROM `user` WHERE user_name = ?) " +
		"ORDER BY LENGTH(topic) DESC, CASE WHEN `write` THEN 1 ELSE 0 END DESC, CASE WHEN `read` THEN 1 ELSE 0 END DESC, topic"
	mysqlSelectUserReservationsQuery = "" +
		"SELECT a_user.topic, a_user.`read`, a_user.`write`, a_everyone.`read` AS everyone_read, a_everyone.`write` AS everyone_write " +
		"FROM user_access a_user " +
		"LEFT JOIN user_access a_everyone ON a_user.topic = a_everyone.topic AND a_everyone.user_id = (SELECT id FROM `user` WHERE user_name = ?) " +
		"WHERE a_user.user_id = a_user.owner_user_id " +
		"  AND a_user.owner_user_id = (SELECT id FROM `user` WHERE user_name = ?) " +
		"ORDER BY a_user.topic"
	mysqlSelectUserReservationsCountQuery = "" +
		"SELECT COUNT(*) FROM user_access " +
		"WHERE user_id = owner_user_id AND owner_user_id = (SELECT id FROM `user` WHERE user_name = ?)"
	mysqlSelectUserReservationsOwnerQuery = "" +
		"SELECT owner_user_id FROM user_access " +
		"WHERE topic = ? AND user_id = owner_user_id"
	mysqlSelectUserHasReservationQuery = "" +
		"SELECT COUNT(*) FROM user_access " +
		"WHERE user_id = owner_user_id " +
		"  AND owner_user_id = (SELECT id FROM `user` WHERE user_name = ?) " +
		"  AND topic = ?"
	mysqlSelectOtherAccessCountQuery = "" +
		"SELECT COUNT(*) FROM user_access " +
		"WHERE (topic = ? OR ? LIKE topic ESCAPE '\\\\') " +
		"  AND (owner_user_id IS NULL OR owner_user_id != (SELECT id FROM `user` WHERE user_name = ?))"
	mysqlUpsertUserAccessQuery = "" +
		"INSERT INTO user_access (user_id, topic, `read`, `write`, owner_user_id, provisioned) " +
		"VALUES (" +
		"  (SELECT id FROM `user` WHERE user_name = ?)," +
		"  ?, ?, ?, " +
		"  CASE WHEN ? = '' THEN NULL ELSE (SELECT id FROM `user` WHERE user_name = ?) END, " +
		"  ?" +
		") AS new " +
		"ON DUPLICATE KEY UPDATE `read`=new.`read`, `write`=new.`write`, owner_user_id=new.owner_user_id, provisioned=new.provisioned"
	mysqlDeleteUserAccessQuery = "" +
		"DELETE FROM user_access " +
		"WHERE user_id = (SELECT id FROM `user` WHERE user_name = ?) " +
		"   OR owner_user_id = (SELECT id FROM `user` WHERE user_name = ?)"
	mysqlDeleteUserAccessProvisionedQuery = "DELETE FROM user_access WHERE provisioned = TRUE"
	mysqlDeleteTopicAccessQuery           = "" +
		"DELETE FROM user_access " +
		"WHERE (user_id = (SELECT id FROM `user` WHERE user_name = ?) OR owner_user_id = (SELECT id FROM `user` WHERE user_name = ?)) " +
		"  AND topic = ?"
	mysqlDeleteAllAccessQuery = "DELETE FROM user_access"

	// Token queries
	mysqlSelectTokenQuery                = "SELECT token, label, last_access, last_origin, expires, provisioned FROM user_token WHERE user_id = ? AND token = ?"
	mysqlSelectTokensQuery               = "SELECT token, label, last_access, last_origin, expires, provisioned FROM user_token WHERE user_id = ?"
	mysqlSelectTokenCountQuery           = "SELECT COUNT(*) FROM user_token WHERE user_id = ?"
	mysqlSelectAllProvisionedTokensQuery = "SELECT token, label, last_access, last_origin, expires, provisioned FROM user_token WHERE provisioned = TRUE"
	mysqlUpsertTokenQuery                = "" +
		"INSERT INTO user_token (user_id, token, label, last_access, last_origin, expires, provisioned) " +
		"VALUES (?, ?, ?, ?, ?, ?, ?) AS new " +
		"ON DUPLICATE KEY UPDATE label = new.label, expires = new.expires, provisioned = new.provisioned"
	mysqlUpdateTokenQuery                = "UPDATE user_token SET label = ?, expires = ? WHERE user_id = ? AND token = ?"
	mysqlUpdateTokenLastAccessQuery      = "UPDATE user_token SET last_access = ?, last_origin = ? WHERE token = ?"
	mysqlDeleteTokenQuery                = "DELETE FROM user_token WHERE user_id = ? AND token = ?"
	mysqlDeleteProvisionedTokenQuery     = "DELETE FROM user_token WHERE token = ?"
	mysqlDeleteAllProvisionedTokensQuery = "DELETE FROM user_token WHERE provisioned = TRUE"
	mysqlDeleteAllTokenQuery             = "DELETE FROM user_token WHERE user_id = ?"
	mysqlDeleteExpiredTokensQuery        = "DELETE FROM user_token WHERE expires > 0 AND expires < ?"
	// MySQL's optimizer rejects self-referential subqueries against the same
	// table being modified ("You can't specify target table 'user_token' for
	// update in FROM clause"). The fix is to wrap the inner SELECT in another
	// SELECT so it materializes as a derived table.
	mysqlDeleteExcessTokensQuery = "" +
		"DELETE FROM user_token " +
		"WHERE user_id = ? " +
		"  AND (user_id, token) NOT IN (" +
		"    SELECT user_id, token FROM (" +
		"      SELECT user_id, token FROM user_token WHERE user_id = ? ORDER BY expires DESC LIMIT ?" +
		"    ) AS keep_tokens" +
		"  )"

	// Tier queries
	mysqlInsertTierQuery = `
		INSERT INTO tier (id, code, name, messages_limit, messages_expiry_duration, emails_limit, calls_limit, reservations_limit, attachment_file_size_limit, attachment_total_size_limit, attachment_expiry_duration, attachment_bandwidth_limit, stripe_monthly_price_id, stripe_yearly_price_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	mysqlUpdateTierQuery = `
		UPDATE tier
		SET name = ?, messages_limit = ?, messages_expiry_duration = ?, emails_limit = ?, calls_limit = ?, reservations_limit = ?, attachment_file_size_limit = ?, attachment_total_size_limit = ?, attachment_expiry_duration = ?, attachment_bandwidth_limit = ?, stripe_monthly_price_id = ?, stripe_yearly_price_id = ?
		WHERE code = ?
	`
	mysqlSelectTiersQuery = `
		SELECT id, code, name, messages_limit, messages_expiry_duration, emails_limit, calls_limit, reservations_limit, attachment_file_size_limit, attachment_total_size_limit, attachment_expiry_duration, attachment_bandwidth_limit, stripe_monthly_price_id, stripe_yearly_price_id
		FROM tier
		ORDER BY _seq
	`
	mysqlSelectTierByCodeQuery = `
		SELECT id, code, name, messages_limit, messages_expiry_duration, emails_limit, calls_limit, reservations_limit, attachment_file_size_limit, attachment_total_size_limit, attachment_expiry_duration, attachment_bandwidth_limit, stripe_monthly_price_id, stripe_yearly_price_id
		FROM tier
		WHERE code = ?
	`
	mysqlSelectTierByPriceIDQuery = `
		SELECT id, code, name, messages_limit, messages_expiry_duration, emails_limit, calls_limit, reservations_limit, attachment_file_size_limit, attachment_total_size_limit, attachment_expiry_duration, attachment_bandwidth_limit, stripe_monthly_price_id, stripe_yearly_price_id
		FROM tier
		WHERE (stripe_monthly_price_id = ? OR stripe_yearly_price_id = ?)
	`
	mysqlDeleteTierQuery = `DELETE FROM tier WHERE code = ?`

	// Phone queries
	mysqlSelectPhoneNumbersQuery = `SELECT phone_number FROM user_phone WHERE user_id = ?`
	mysqlInsertPhoneNumberQuery  = `INSERT INTO user_phone (user_id, phone_number) VALUES (?, ?)`
	mysqlDeletePhoneNumberQuery  = `DELETE FROM user_phone WHERE user_id = ? AND phone_number = ?`

	// Email queries
	mysqlSelectEmailsQuery = `SELECT email FROM user_email WHERE user_id = ? ORDER BY email`
	mysqlInsertEmailQuery  = `INSERT INTO user_email (user_id, email) VALUES (?, ?)`
	mysqlDeleteEmailQuery  = `DELETE FROM user_email WHERE user_id = ? AND email = ?`

	// Billing queries
	mysqlUpdateBillingQuery = "" +
		"UPDATE `user` " +
		"SET stripe_customer_id = ?, stripe_subscription_id = ?, stripe_subscription_status = ?, stripe_subscription_interval = ?, stripe_subscription_paid_until = ?, stripe_subscription_cancel_at = ? " +
		"WHERE user_name = ?"
)

var mysqlQueries = queries{
	selectUserByID:               mysqlSelectUserByIDQuery,
	selectUserByName:             mysqlSelectUserByNameQuery,
	selectUserByToken:            mysqlSelectUserByTokenQuery,
	selectUserByStripeCustomerID: mysqlSelectUserByStripeCustomerIDQuery,
	selectUsernames:              mysqlSelectUsernamesQuery,
	selectUsers:                  mysqlSelectUsersQuery,
	selectUserCount:              mysqlSelectUserCountQuery,
	selectUserIDFromUsername:     mysqlSelectUserIDFromUsernameQuery,
	insertUser:                   mysqlInsertUserQuery,
	updateUserPass:               mysqlUpdateUserPassQuery,
	updateUserRole:               mysqlUpdateUserRoleQuery,
	updateUserProvisioned:        mysqlUpdateUserProvisionedQuery,
	updateUserPrefs:              mysqlUpdateUserPrefsQuery,
	updateUserStats:              mysqlUpdateUserStatsQuery,
	updateUserStatsResetAll:      mysqlUpdateUserStatsResetAllQuery,
	updateUserTier:               mysqlUpdateUserTierQuery,
	updateUserDeleted:            mysqlUpdateUserDeletedQuery,
	deleteUser:                   mysqlDeleteUserQuery,
	deleteUserTier:               mysqlDeleteUserTierQuery,
	deleteUsersMarked:            mysqlDeleteUsersMarkedQuery,
	deleteUsersProvisioned:       mysqlDeleteUsersProvisionedQuery,
	selectTopicPerms:             mysqlSelectTopicPermsQuery,
	selectUserAllAccess:          mysqlSelectUserAllAccessQuery,
	selectUserAccess:             mysqlSelectUserAccessQuery,
	selectUserReservations:       mysqlSelectUserReservationsQuery,
	selectUserReservationsCount:  mysqlSelectUserReservationsCountQuery,
	selectUserReservationsOwner:  mysqlSelectUserReservationsOwnerQuery,
	selectUserHasReservation:     mysqlSelectUserHasReservationQuery,
	selectOtherAccessCount:       mysqlSelectOtherAccessCountQuery,
	upsertUserAccess:             mysqlUpsertUserAccessQuery,
	deleteUserAccess:             mysqlDeleteUserAccessQuery,
	deleteUserAccessProvisioned:  mysqlDeleteUserAccessProvisionedQuery,
	deleteTopicAccess:            mysqlDeleteTopicAccessQuery,
	deleteAllAccess:              mysqlDeleteAllAccessQuery,
	selectToken:                  mysqlSelectTokenQuery,
	selectTokens:                 mysqlSelectTokensQuery,
	selectTokenCount:             mysqlSelectTokenCountQuery,
	selectAllProvisionedTokens:   mysqlSelectAllProvisionedTokensQuery,
	upsertToken:                  mysqlUpsertTokenQuery,
	updateToken:                  mysqlUpdateTokenQuery,
	updateTokenLastAccess:        mysqlUpdateTokenLastAccessQuery,
	deleteToken:                  mysqlDeleteTokenQuery,
	deleteProvisionedToken:       mysqlDeleteProvisionedTokenQuery,
	deleteAllProvisionedTokens:   mysqlDeleteAllProvisionedTokensQuery,
	deleteAllToken:               mysqlDeleteAllTokenQuery,
	deleteExpiredTokens:          mysqlDeleteExpiredTokensQuery,
	deleteExcessTokens:           mysqlDeleteExcessTokensQuery,
	insertTier:                   mysqlInsertTierQuery,
	selectTiers:                  mysqlSelectTiersQuery,
	selectTierByCode:             mysqlSelectTierByCodeQuery,
	selectTierByPriceID:          mysqlSelectTierByPriceIDQuery,
	updateTier:                   mysqlUpdateTierQuery,
	deleteTier:                   mysqlDeleteTierQuery,
	selectPhoneNumbers:           mysqlSelectPhoneNumbersQuery,
	insertPhoneNumber:            mysqlInsertPhoneNumberQuery,
	deletePhoneNumber:            mysqlDeletePhoneNumberQuery,
	selectEmails:                 mysqlSelectEmailsQuery,
	insertEmail:                  mysqlInsertEmailQuery,
	deleteEmail:                  mysqlDeleteEmailQuery,
	updateBilling:                mysqlUpdateBillingQuery,
}

// NewMySQLManager creates a new Manager backed by a MySQL database using an
// existing connection pool. The pool must point at MySQL 8.0.20 or newer (the
// version check is done at db/mysql.Open time, not here).
func NewMySQLManager(d *db.DB, config *Config) (*Manager, error) {
	if err := setupMySQLUser(d.Primary()); err != nil {
		return nil, err
	}
	return newManager(d, mysqlQueries, config)
}
