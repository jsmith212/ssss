// Package audio decodes audio files to float32 PCM, reads WAV/AIFF chunk
// metadata, computes mini and full-resolution peaks, and serves the /audio and
// /peaks HTTP endpoints.
//
// Formats are recognised by content, never by file extension. WAV and AIFF are
// read by this package's own chunk walkers, which tolerate the header mistakes
// common in sample packs; FLAC, MP3 and Ogg Vorbis go through pure-Go decoders.
// Third-party decoders are run behind a recover, so a corrupt file yields an
// error rather than a panic.
package audio
