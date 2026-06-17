// Package voice is a small, self-contained Discord voice client used to play
// audio (e.g. text-to-speech) into a guild voice channel.
//
// Why this exists instead of using discordgo's voice support: discordgo only
// implements the legacy xsalsa20_poly1305 transport encryption mode, which
// Discord deprecated and stopped accepting. As a result discordgo can connect
// to a voice channel but its audio is silently dropped. This package implements
// the voice gateway (version 8) handshake and the UDP RTP media path using the
// AEAD encryption modes Discord now requires (aead_aes256_gcm_rtpsize, with
// aead_xchacha20_poly1305_rtpsize as the mandatory fallback).
//
// It is transport only: the caller is responsible for sending the gateway
// "voice state update" (Op 4) on the main bot gateway and for handing this
// package the resulting session id, token and endpoint. discordgo's
// (*Session).ChannelVoiceJoinManual does exactly that.
//
// Audio is supplied as raw Opus frames (48kHz). This package does not encode
// Opus; callers feed it pre-encoded frames, e.g. demuxed from an OGG/Opus
// stream via DemuxOggOpus.
//
// DAVE (Discord's MLS-based end-to-end voice encryption) is deliberately not
// implemented: Identify advertises max_dave_protocol_version 0, so Discord
// keeps the connection on the plain (transport-encrypted only) path and the
// whole protocol stays JSON.
package voice
