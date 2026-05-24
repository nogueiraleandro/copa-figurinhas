package model

import "time"

// Participant is a guest / sticker in the album.
type Participant struct {
	ID              int64
	Token           string // random hex, used in QR URLs
	Name            string
	Nickname        string
	PhotoPath       string
	Team            string // ex: "PALMEIRAS (BR)" — escrito na figurinha
	InfoDate        string // DATA da linha de dados
	Height          string // ALTURA da linha de dados
	Weight          string // Kg da linha de dados
	Phrase          string // frase alternativa à linha de dados (<=25 chars)
	Active          bool
	ClaimedDeviceID *int64
	CreatedAt       time.Time
}

// Device represents a phone (browser session).
type Device struct {
	ID            int64
	CookieToken   string // set in persistent cookie
	ParticipantID int64  // owner
	CreatedAt     time.Time
}

// Collection is one entry in a participant's sticker album.
type Collection struct {
	OwnerID     int64
	StickerID   int64
	CollectedAt time.Time
}

// Setting holds all key-value configuration.
type Setting struct {
	BaseURL           string
	KickoffAt         *time.Time
	RosterLocked      bool
	AdminPasswordHash string // bcrypt
	GeminiAPIKey      string
	AIModel           string
	AIPrompt          string
	AIReferencePath   string
}

// RankEntry is used for ranking queries.
type RankEntry struct {
	ParticipantID int64
	Name          string
	Nickname      string
	PhotoPath     string
	Count         int
	Total         int
	Complete      bool
	CompletedAt   *time.Time
	MaxReachedAt  *time.Time // time when count became current max (for tiebreak)
}
