package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	idb "copa/internal/db"
	"copa/internal/model"
)

// ErrNotFound is returned when a record doesn't exist.
var ErrNotFound = errors.New("not found")

// ErrRosterLocked is returned when trying to add while roster is locked.
var ErrRosterLocked = errors.New("roster is locked")

// Store encapsulates all database operations.
type Store struct {
	db *sql.DB
}

// NewStore creates a Store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// ---- Helpers ----

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ---- Settings ----

func (s *Store) GetSetting() (model.Setting, error) {
	row := s.db.QueryRow(`
		SELECT base_url, kickoff_at, roster_locked, admin_password_hash,
		       gemini_api_key, ai_model, ai_prompt, ai_reference_path
		FROM setting WHERE id=1`)
	var set model.Setting
	var kickoffStr sql.NullString
	if err := row.Scan(
		&set.BaseURL, &kickoffStr, &set.RosterLocked, &set.AdminPasswordHash,
		&set.GeminiAPIKey, &set.AIModel, &set.AIPrompt, &set.AIReferencePath,
	); err != nil {
		return set, err
	}
	if kickoffStr.Valid && kickoffStr.String != "" {
		t, err := idb.StringToTime(kickoffStr.String)
		if err == nil {
			set.KickoffAt = &t
		}
	}
	return set, nil
}

func (s *Store) SaveSetting(set model.Setting) error {
	var kickoffStr *string
	if set.KickoffAt != nil {
		v := idb.TimeToString(*set.KickoffAt)
		kickoffStr = &v
	}
	_, err := s.db.Exec(`
		UPDATE setting
		SET base_url=?, kickoff_at=?, roster_locked=?, admin_password_hash=?,
		    gemini_api_key=?, ai_model=?, ai_prompt=?, ai_reference_path=?
		WHERE id=1`,
		set.BaseURL, kickoffStr, boolToInt(set.RosterLocked), set.AdminPasswordHash,
		set.GeminiAPIKey, set.AIModel, set.AIPrompt, set.AIReferencePath)
	return err
}

// ---- Participants ----

func (s *Store) CreateParticipant(name, nickname, photoPath string) (*model.Participant, error) {
	token, err := randomHex(8) // 16-char hex token
	if err != nil {
		return nil, err
	}
	now := idb.TimeToString(time.Now())
	res, err := s.db.Exec(`INSERT INTO participant (token, name, nickname, photo_path, active, created_at)
		VALUES (?, ?, ?, ?, 1, ?)`, token, name, nickname, photoPath, now)
	if err != nil {
		return nil, fmt.Errorf("insert participant: %w", err)
	}
	id, _ := res.LastInsertId()
	return &model.Participant{
		ID: id, Token: token, Name: name, Nickname: nickname,
		PhotoPath: photoPath, Active: true, CreatedAt: time.Now(),
	}, nil
}

func (s *Store) GetParticipantByToken(token string) (*model.Participant, error) {
	return s.scanParticipant(s.db.QueryRow(
		`SELECT id, token, name, nickname, photo_path, active, claimed_device_id, created_at
		 FROM participant WHERE token=?`, token))
}

func (s *Store) GetParticipantByID(id int64) (*model.Participant, error) {
	return s.scanParticipant(s.db.QueryRow(
		`SELECT id, token, name, nickname, photo_path, active, claimed_device_id, created_at
		 FROM participant WHERE id=?`, id))
}

func (s *Store) ListParticipants() ([]*model.Participant, error) {
	rows, err := s.db.Query(`SELECT id, token, name, nickname, photo_path, active, claimed_device_id, created_at
		FROM participant ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Participant
	for rows.Next() {
		p, err := s.scanParticipantRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ListActiveParticipants() ([]*model.Participant, error) {
	rows, err := s.db.Query(`SELECT id, token, name, nickname, photo_path, active, claimed_device_id, created_at
		FROM participant WHERE active=1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Participant
	for rows.Next() {
		p, err := s.scanParticipantRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CountActiveParticipants() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM participant WHERE active=1`).Scan(&n)
	return n, err
}

func (s *Store) UpdateParticipant(p *model.Participant) error {
	_, err := s.db.Exec(`UPDATE participant SET name=?, nickname=?, photo_path=?, active=? WHERE id=?`,
		p.Name, p.Nickname, p.PhotoPath, boolToInt(p.Active), p.ID)
	return err
}

func (s *Store) SetParticipantActive(id int64, active bool) error {
	_, err := s.db.Exec(`UPDATE participant SET active=? WHERE id=?`, boolToInt(active), id)
	return err
}

func (s *Store) scanParticipant(row *sql.Row) (*model.Participant, error) {
	var p model.Participant
	var deviceID sql.NullInt64
	var createdStr string
	var activeInt int
	err := row.Scan(&p.ID, &p.Token, &p.Name, &p.Nickname, &p.PhotoPath, &activeInt, &deviceID, &createdStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Active = activeInt == 1
	if deviceID.Valid {
		p.ClaimedDeviceID = &deviceID.Int64
	}
	p.CreatedAt, _ = idb.StringToTime(createdStr)
	return &p, nil
}

func (s *Store) scanParticipantRow(rows *sql.Rows) (*model.Participant, error) {
	var p model.Participant
	var deviceID sql.NullInt64
	var createdStr string
	var activeInt int
	err := rows.Scan(&p.ID, &p.Token, &p.Name, &p.Nickname, &p.PhotoPath, &activeInt, &deviceID, &createdStr)
	if err != nil {
		return nil, err
	}
	p.Active = activeInt == 1
	if deviceID.Valid {
		p.ClaimedDeviceID = &deviceID.Int64
	}
	p.CreatedAt, _ = idb.StringToTime(createdStr)
	return &p, nil
}

// ---- Devices ----

func (s *Store) CreateDevice(participantID int64) (*model.Device, error) {
	cookieToken, err := randomHex(16) // 32-char cookie
	if err != nil {
		return nil, err
	}
	now := idb.TimeToString(time.Now())
	res, err := s.db.Exec(`INSERT INTO device (cookie_token, participant_id, created_at) VALUES (?, ?, ?)`,
		cookieToken, participantID, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()

	// Link device to participant
	_, err = s.db.Exec(`UPDATE participant SET claimed_device_id=? WHERE id=?`, id, participantID)
	if err != nil {
		return nil, err
	}
	return &model.Device{ID: id, CookieToken: cookieToken, ParticipantID: participantID, CreatedAt: time.Now()}, nil
}

func (s *Store) GetDeviceByCookie(cookieToken string) (*model.Device, error) {
	row := s.db.QueryRow(`SELECT id, cookie_token, participant_id, created_at FROM device WHERE cookie_token=?`, cookieToken)
	var d model.Device
	var createdStr string
	err := row.Scan(&d.ID, &d.CookieToken, &d.ParticipantID, &createdStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.CreatedAt, _ = idb.StringToTime(createdStr)
	return &d, nil
}

// ReassignDevice updates a device to point to a different participant (recovery flow).
func (s *Store) ReassignDevice(deviceID, participantID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	// Unlink any old device from this participant
	_, err = tx.Exec(`UPDATE participant SET claimed_device_id=NULL WHERE claimed_device_id=?`, deviceID)
	if err != nil {
		return err
	}
	// Point device to new participant
	_, err = tx.Exec(`UPDATE device SET participant_id=? WHERE id=?`, participantID, deviceID)
	if err != nil {
		return err
	}
	// Link participant to device
	_, err = tx.Exec(`UPDATE participant SET claimed_device_id=? WHERE id=?`, deviceID, participantID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ---- Collection ----

// AddToCollection adds a sticker to owner's collection. Idempotent (UNIQUE constraint).
// Returns (isNew, error).
func (s *Store) AddToCollection(ownerID, stickerID int64) (bool, error) {
	now := idb.TimeToString(time.Now())
	res, err := s.db.Exec(`INSERT OR IGNORE INTO collection (owner_id, sticker_id, collected_at) VALUES (?, ?, ?)`,
		ownerID, stickerID, now)
	if err != nil {
		return false, fmt.Errorf("insert collection: %w", err)
	}
	rows, _ := res.RowsAffected()
	return rows > 0, nil
}

func (s *Store) GetCollection(ownerID int64) ([]*model.Collection, error) {
	rows, err := s.db.Query(`SELECT owner_id, sticker_id, collected_at FROM collection WHERE owner_id=? ORDER BY collected_at`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Collection
	for rows.Next() {
		var c model.Collection
		var collectedStr string
		if err := rows.Scan(&c.OwnerID, &c.StickerID, &collectedStr); err != nil {
			return nil, err
		}
		c.CollectedAt, _ = idb.StringToTime(collectedStr)
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (s *Store) HasSticker(ownerID, stickerID int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM collection WHERE owner_id=? AND sticker_id=?`, ownerID, stickerID).Scan(&n)
	return n > 0, err
}

func (s *Store) CountCollection(ownerID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM collection WHERE owner_id=?`, ownerID).Scan(&n)
	return n, err
}

// CountActiveCollected counts only stickers of ACTIVE participants in the owner's album.
// Used for the progress bar so it never exceeds the active total.
func (s *Store) CountActiveCollected(ownerID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM collection c
		INNER JOIN participant p ON p.id = c.sticker_id
		WHERE c.owner_id=? AND p.active=1`, ownerID).Scan(&n)
	return n, err
}

// IsComplete returns whether ownerID has collected all active participants.
func (s *Store) IsComplete(ownerID int64) (bool, error) {
	total, err := s.CountActiveParticipants()
	if err != nil {
		return false, err
	}
	if total == 0 {
		return false, nil
	}
	// Count how many active stickers this owner has
	var collected int
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM collection c
		INNER JOIN participant p ON p.id = c.sticker_id
		WHERE c.owner_id=? AND p.active=1`, ownerID).Scan(&collected)
	if err != nil {
		return false, err
	}
	return collected >= total, nil
}

// CompletedAt returns the timestamp when a user first completed the album (nil if not complete).
func (s *Store) CompletedAt(ownerID int64) (*time.Time, error) {
	// The completion time is the collected_at of the last sticker added that made them complete.
	// We detect this by finding if count == total and returning the MAX collected_at.
	complete, err := s.IsComplete(ownerID)
	if err != nil || !complete {
		return nil, err
	}
	var ts string
	err = s.db.QueryRow(`SELECT MAX(collected_at) FROM collection WHERE owner_id=?`, ownerID).Scan(&ts)
	if err != nil {
		return nil, err
	}
	t, err := idb.StringToTime(ts)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetRanking returns ranking for all participants who have at least claimed identity.
func (s *Store) GetRanking() ([]*model.RankEntry, error) {
	return s.rankingAsOf(nil)
}

// GetFinalRanking returns the standings frozen at the given moment (kickoff).
// Stickers collected after asOf are excluded, so the official result is deterministic.
func (s *Store) GetFinalRanking(asOf time.Time) ([]*model.RankEntry, error) {
	return s.rankingAsOf(&asOf)
}

// rankingAsOf computes the ranking, optionally only counting collections up to asOf.
func (s *Store) rankingAsOf(asOf *time.Time) ([]*model.RankEntry, error) {
	total, err := s.CountActiveParticipants()
	if err != nil {
		return nil, err
	}

	// collected_at is stored as RFC3339Nano UTC, so string comparison is chronological.
	cutoffClause := ""
	var args []interface{}
	if asOf != nil {
		cutoffClause = "WHERE c.collected_at <= ?"
		args = append(args, idb.TimeToString(*asOf))
	}

	rows, err := s.db.Query(fmt.Sprintf(`
		SELECT p.id, p.name, p.nickname, p.photo_path,
		       COALESCE(col.cnt, 0) as cnt,
		       COALESCE(col.max_at, '') as max_at
		FROM participant p
		LEFT JOIN (
			SELECT c.owner_id,
			       COUNT(*) as cnt,
			       MAX(c.collected_at) as max_at
			FROM collection c
			INNER JOIN participant ps ON ps.id = c.sticker_id AND ps.active=1
			%s
			GROUP BY c.owner_id
		) col ON col.owner_id = p.id
		WHERE p.active=1 AND p.claimed_device_id IS NOT NULL
		ORDER BY cnt DESC, max_at ASC
	`, cutoffClause), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.RankEntry
	for rows.Next() {
		var e model.RankEntry
		var maxAtStr string
		if err := rows.Scan(&e.ParticipantID, &e.Name, &e.Nickname, &e.PhotoPath, &e.Count, &maxAtStr); err != nil {
			return nil, err
		}
		e.Total = total
		e.Complete = e.Count >= total && total > 0
		if maxAtStr != "" {
			t, _ := idb.StringToTime(maxAtStr)
			e.MaxReachedAt = &t
		}
		out = append(out, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Fechar o cursor ANTES de novas queries: com MaxOpenConns(1), consultar
	// CompletedAt com o cursor aberto causa deadlock pela conexao unica.
	rows.Close()
	for _, e := range out {
		if e.Complete {
			// Para album completo, MaxReachedAt ja e o instante em que completou.
			e.CompletedAt = e.MaxReachedAt
		}
	}
	return out, nil
}

// Winner determines the tournament champion as of asOf (nil = now/live):
//   - among those who completed the album, the FIRST to complete wins;
//   - otherwise the one with the most stickers (ties broken by who reached it first).
//
// Returns nil if there are no ranked participants.
func (s *Store) Winner(asOf *time.Time) (*model.RankEntry, error) {
	rank, err := s.rankingAsOf(asOf)
	if err != nil {
		return nil, err
	}
	if len(rank) == 0 {
		return nil, nil
	}
	var first *model.RankEntry
	for _, e := range rank {
		if !e.Complete || e.CompletedAt == nil {
			continue
		}
		if first == nil || e.CompletedAt.Before(*first.CompletedAt) {
			first = e
		}
	}
	if first != nil {
		return first, nil
	}
	// Ninguem completou: ranking ja esta ordenado por cnt DESC, max_at ASC.
	return rank[0], nil
}

// GetStoredFinalRanking returns the persisted final ranking snapshot.
func (s *Store) GetStoredFinalRanking() ([]*model.RankEntry, *time.Time, error) {
	var frozenStr string
	err := s.db.QueryRow(`SELECT frozen_at FROM final_snapshot WHERE id=1`).Scan(&frozenStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	frozenAt, err := idb.StringToTime(frozenStr)
	if err != nil {
		return nil, nil, err
	}

	rows, err := s.db.Query(`
		SELECT participant_id, name, nickname, photo_path, count, total, complete,
		       COALESCE(max_reached_at, ''), COALESCE(completed_at, '')
		FROM final_ranking
		ORDER BY position`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var out []*model.RankEntry
	for rows.Next() {
		var e model.RankEntry
		var completeInt int
		var maxAtStr, completedAtStr string
		if err := rows.Scan(
			&e.ParticipantID, &e.Name, &e.Nickname, &e.PhotoPath,
			&e.Count, &e.Total, &completeInt, &maxAtStr, &completedAtStr,
		); err != nil {
			return nil, nil, err
		}
		e.Complete = completeInt == 1
		if maxAtStr != "" {
			t, _ := idb.StringToTime(maxAtStr)
			e.MaxReachedAt = &t
		}
		if completedAtStr != "" {
			t, _ := idb.StringToTime(completedAtStr)
			e.CompletedAt = &t
		}
		out = append(out, &e)
	}
	return out, &frozenAt, rows.Err()
}

// EnsureFinalSnapshot persists the official ranking as of asOf and returns it.
// If the stored snapshot was made for a different apito, it is replaced.
func (s *Store) EnsureFinalSnapshot(asOf time.Time) ([]*model.RankEntry, error) {
	if existing, frozenAt, err := s.GetStoredFinalRanking(); err == nil {
		if frozenAt != nil && idb.TimeToString(*frozenAt) == idb.TimeToString(asOf) {
			return existing, nil
		}
		if err := s.ClearFinalSnapshot(); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	ranking, err := s.GetFinalRanking(asOf)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	now := idb.TimeToString(time.Now())
	res, err := tx.Exec(`INSERT OR IGNORE INTO final_snapshot (id, frozen_at, created_at) VALUES (1, ?, ?)`,
		idb.TimeToString(asOf), now)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		existing, _, err := s.GetStoredFinalRanking()
		return existing, err
	}

	for i, e := range ranking {
		var maxAt, completedAt interface{}
		if e.MaxReachedAt != nil {
			maxAt = idb.TimeToString(*e.MaxReachedAt)
		}
		if e.CompletedAt != nil {
			completedAt = idb.TimeToString(*e.CompletedAt)
		}
		if _, err := tx.Exec(`
			INSERT INTO final_ranking
				(position, participant_id, name, nickname, photo_path, count, total, complete, max_reached_at, completed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			i+1, e.ParticipantID, e.Name, e.Nickname, e.PhotoPath, e.Count, e.Total,
			boolToInt(e.Complete), maxAt, completedAt,
		); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ranking, nil
}

// ClearFinalSnapshot removes the persisted official ranking.
func (s *Store) ClearFinalSnapshot() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`DELETE FROM final_ranking`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM final_snapshot`); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- Transfer / Admin helpers ----

// TransferCollection transfers all collection entries from srcID to dstID.
func (s *Store) TransferCollection(srcID, dstID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Get src entries that dst doesn't have
	rows, err := tx.Query(`SELECT sticker_id, collected_at FROM collection WHERE owner_id=?`, srcID)
	if err != nil {
		return err
	}
	type entry struct {
		sid int64
		at  string
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.sid, &e.at); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	rows.Close()

	for _, e := range entries {
		_, err := tx.Exec(`INSERT OR IGNORE INTO collection (owner_id, sticker_id, collected_at) VALUES (?, ?, ?)`,
			dstID, e.sid, e.at)
		if err != nil {
			return err
		}
	}
	// Delete src entries
	_, err = tx.Exec(`DELETE FROM collection WHERE owner_id=?`, srcID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// SetAdminPasswordHash stores a pre-hashed admin password (used for env-configured password).
func (s *Store) SetAdminPasswordHash(hash string) error {
	_, err := s.db.Exec(`UPDATE setting SET admin_password_hash=? WHERE id=1`, hash)
	return err
}

// BackupTo writes a consistent snapshot of the database to dstPath using VACUUM INTO.
// Unlike copying the .db file, this is safe with WAL mode (no half-written pages).
func (s *Store) BackupTo(dstPath string) error {
	// VACUUM INTO requires the destination not to exist.
	_ = os.Remove(dstPath)
	_, err := s.db.Exec(`VACUUM INTO ?`, dstPath)
	return err
}

// Checkpoint flushes the WAL into the main database file (call before shutdown).
func (s *Store) Checkpoint() error {
	_, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// ResetGameData apaga participantes, devices e coleções (dados do jogo/ensaio),
// preservando base_url e a senha de admin para não trancar o operador para fora.
// Tambem zera kickoff_at e roster_locked, e reinicia os autoincrement.
func (s *Store) ResetGameData() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Ordem respeita as FKs (collection referencia participant; device idem).
	stmts := []string{
		`DELETE FROM final_ranking`,
		`DELETE FROM final_snapshot`,
		`DELETE FROM collection`,
		`UPDATE participant SET claimed_device_id=NULL`,
		`DELETE FROM device`,
		`DELETE FROM participant`,
		`UPDATE setting SET kickoff_at=NULL, roster_locked=0 WHERE id=1`,
		`DELETE FROM sqlite_sequence WHERE name IN ('participant','device')`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("reset (%s): %w", q, err)
		}
	}
	return tx.Commit()
}

// RestoreFrom substitui TODO o conteudo do banco pelo de um backup (.db) gerado
// por BackupTo/VACUUM INTO. Mantem o *sql.DB vivo (sem reiniciar o servidor):
// copia as linhas de cada tabela via ATTACH, numa unica conexao.
//
// Atencao: as fotos ficam em uploads/ e NAO sao restauradas aqui; e a senha de
// admin passa a ser a do backup.
func (s *Store) RestoreFrom(srcPath string) error {
	// 1) Validar que o arquivo parece um banco do copa.
	vdb, err := sql.Open("sqlite", srcPath+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("abrir backup: %w", err)
	}
	var n int
	verr := vdb.QueryRow(`SELECT COUNT(*) FROM setting`).Scan(&n)
	vdb.Close()
	if verr != nil {
		return fmt.Errorf("arquivo não parece um backup válido do Copa: %w", verr)
	}

	// 2) Operar numa unica conexao (ATTACH/PRAGMA/tx coerentes; MaxOpenConns(1)).
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	// Garante religar as FKs mesmo em caso de erro.
	defer conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`) //nolint:errcheck

	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS src`, srcPath); err != nil {
		return fmt.Errorf("attach backup: %w", err)
	}
	defer conn.ExecContext(ctx, `DETACH DATABASE src`) //nolint:errcheck

	tables := []string{"final_ranking", "final_snapshot", "collection", "device", "participant", "setting"}
	tableExists := map[string]bool{}
	for _, t := range tables {
		ok, err := sourceTableExists(ctx, conn, t)
		if err != nil {
			return err
		}
		tableExists[t] = ok
	}
	sourceSettingCols, err := sourceTableColumns(ctx, conn, "setting")
	if err != nil {
		return err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Limpa e recopia cada tabela a partir do backup. Tabelas novas sao opcionais
	// para que backups antigos continuem restauraveis apos migracoes.
	for _, t := range tables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+t); err != nil {
			tx.Rollback() //nolint:errcheck
			return fmt.Errorf("limpar %s: %w", t, err)
		}
	}
	for _, t := range tables {
		if !tableExists[t] {
			continue
		}
		if t == "setting" {
			if err := copySettingFromSource(ctx, tx, sourceSettingCols); err != nil {
				tx.Rollback() //nolint:errcheck
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+t+` SELECT * FROM src.`+t); err != nil {
			tx.Rollback() //nolint:errcheck
			return fmt.Errorf("restaurar %s: %w", t, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	_, _ = conn.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return nil
}

func copySettingFromSource(ctx context.Context, tx *sql.Tx, sourceCols map[string]bool) error {
	known := []string{
		"id", "base_url", "kickoff_at", "roster_locked", "admin_password_hash",
		"gemini_api_key", "ai_model", "ai_prompt", "ai_reference_path",
	}
	var cols []string
	for _, c := range known {
		if sourceCols[c] {
			cols = append(cols, c)
		}
	}
	if len(cols) == 0 {
		return nil
	}
	q := fmt.Sprintf(`INSERT INTO setting (%s) SELECT %s FROM src.setting`,
		joinQuotedCols(cols), joinQuotedCols(cols))
	if _, err := tx.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("restaurar setting: %w", err)
	}
	return nil
}

func sourceTableExists(ctx context.Context, conn *sql.Conn, table string) (bool, error) {
	var n int
	err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM src.sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
	return n > 0, err
}

func sourceTableColumns(ctx context.Context, conn *sql.Conn, table string) (map[string]bool, error) {
	rows, err := conn.QueryContext(ctx, `PRAGMA src.table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

func joinQuotedCols(cols []string) string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, `"`+c+`"`)
	}
	return strings.Join(out, ",")
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
