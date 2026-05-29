package config

import "time"

// Server configuration
const (
	DefaultListenAddr = ":8080"
	ShutdownTimeout    = 15 * time.Second
)

// Backup configuration
const (
	BackupInterval   = 30 * time.Minute
	MaxBackups       = 5
	MaxBackupAge     = 7 * 24 * time.Hour
)

// Session configuration
const (
	CookieName       = "copa_session"
	CookieDuration   = 365 * 24 * time.Hour
	AdminCookieName  = "copa_admin"
	SessionCleanup   = 5 * time.Minute
)

// SSE and notification configuration
const (
	RankingThrottle = 250 * time.Millisecond
	SSEHeartbeat    = 20 * time.Second
	ChannelBufferSize = 8
)

// QR code configuration
const (
	QRTokenLength = 8  // 16-char hex token
	CookieTokenLength = 16 // 32-char cookie
)

// Admin configuration
const (
	DefaultAIModel = "gemini-3.1-flash-image-preview"
	DefaultAIPrompt = "Transforme a foto em uma figurinha no mesmo estilo da figurinha modelo. Preserve a identidade, rosto, expressao e caracteristicas principais da pessoa. Use composicao limpa, aparencia de figurinha colecionavel, cores vivas, acabamento profissional e fundo compativel com o modelo. Nao copie nomes, datas, medidas ou textos da figurinha modelo."
)

// Participant fields
const (
	MaxPhraseLength = 25 // Alternative phrase max length
)
