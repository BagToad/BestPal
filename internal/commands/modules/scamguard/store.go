package scamguard

import (
	"bufio"
	_ "embed"
	"os"
	"strings"

	"github.com/corona10/goimagehash"
)

// embeddedSeed is the committed seed list of known-bad perceptual hashes. One
// hash string per line; lines starting with '#' and blank lines are ignored.
//
//go:embed seed/known_scam_hashes.txt
var embeddedSeed string

// knownHash is a parsed known-bad hash plus its canonical string form.
type knownHash struct {
	hash *goimagehash.ImageHash
	str  string
}

// parseSeedLines returns the non-comment, non-blank, trimmed lines of content.
func parseSeedLines(content string) []string {
	var out []string
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// loadHashes builds the in-memory cache. Seed hashes (embedded plus an optional
// external file) are inserted into the DB if absent, then the cache is loaded
// from the DB. When no DB is available the seeds are parsed straight into the
// cache so detection still works.
func (m *Module) loadHashes() {
	seeds := parseSeedLines(embeddedSeed)
	if path := m.config.GetScamGuardSeedHashesPath(); path != "" {
		if data, err := os.ReadFile(path); err != nil {
			m.config.Logger.Warnf("scamguard: failed to read seed file %q: %v", path, err)
		} else {
			seeds = append(seeds, parseSeedLines(string(data))...)
		}
	}

	if m.db == nil {
		m.mu.Lock()
		m.hashes = m.hashes[:0]
		for _, s := range seeds {
			h, err := parseHash(s)
			if err != nil {
				m.config.Logger.Warnf("scamguard: skipping invalid seed hash %q: %v", s, err)
				continue
			}
			m.hashes = append(m.hashes, knownHash{hash: h, str: h.ToString()})
		}
		m.mu.Unlock()
		return
	}

	for _, s := range seeds {
		if _, err := parseHash(s); err != nil {
			m.config.Logger.Warnf("scamguard: skipping invalid seed hash %q: %v", s, err)
			continue
		}
		if _, err := m.db.AddScamImageHash(s, "seed", "seed"); err != nil {
			m.config.Logger.Warnf("scamguard: failed to seed hash %q: %v", s, err)
		}
	}
	m.reloadCacheFromDB()
}

// reloadCacheFromDB replaces the in-memory cache with the parsed DB rows.
func (m *Module) reloadCacheFromDB() {
	rows, err := m.db.GetScamImageHashes()
	if err != nil {
		m.config.Logger.Warnf("scamguard: failed to load hashes from DB: %v", err)
		return
	}
	parsed := make([]knownHash, 0, len(rows))
	for _, r := range rows {
		h, err := parseHash(r.Hash)
		if err != nil {
			m.config.Logger.Warnf("scamguard: skipping invalid stored hash %q: %v", r.Hash, err)
			continue
		}
		parsed = append(parsed, knownHash{hash: h, str: r.Hash})
	}
	m.mu.Lock()
	m.hashes = parsed
	m.mu.Unlock()
}

// addKnownHash adds a hash to the DB (if available) and the in-memory cache.
// Returns true when the hash was newly added, false when it was already known.
func (m *Module) addKnownHash(str, addedBy, source string) (bool, error) {
	h, err := parseHash(str)
	if err != nil {
		return false, err
	}
	str = h.ToString()

	if m.db != nil {
		inserted, err := m.db.AddScamImageHash(str, addedBy, source)
		if err != nil {
			return false, err
		}
		if !inserted {
			return false, nil
		}
		m.mu.Lock()
		m.hashes = append(m.hashes, knownHash{hash: h, str: str})
		m.mu.Unlock()
		return true, nil
	}

	// No DB: dedupe and append under a single write lock so concurrent callers
	// can't both insert the same hash.
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, kh := range m.hashes {
		if kh.str == str {
			return false, nil
		}
	}
	m.hashes = append(m.hashes, knownHash{hash: h, str: str})
	return true, nil
}

// matchHash reports whether h is within threshold Hamming distance of any known
// bad hash, returning the matched hash string.
func (m *Module) matchHash(h *goimagehash.ImageHash, threshold int) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, kh := range m.hashes {
		dist, err := h.Distance(kh.hash)
		if err != nil {
			continue
		}
		if dist <= threshold {
			return kh.str, true
		}
	}
	return "", false
}

// matchingHashes returns the string form of every known-bad hash within
// threshold Hamming distance of h. Unlike matchHash it does not stop at the
// first match; it is used by the unmark command to clear an image's blocklist
// entries even when more than one near-duplicate was stored.
func (m *Module) matchingHashes(h *goimagehash.ImageHash, threshold int) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	for _, kh := range m.hashes {
		dist, err := h.Distance(kh.hash)
		if err != nil {
			continue
		}
		if dist <= threshold {
			out = append(out, kh.str)
		}
	}
	return out
}

// removeKnownHash deletes an exact hash from the DB (if available) and the
// in-memory cache. Returns true when the hash was present in either store.
func (m *Module) removeKnownHash(str string) (bool, error) {
	if h, err := parseHash(str); err == nil {
		str = h.ToString()
	}

	dbRemoved := false
	if m.db != nil {
		removed, err := m.db.RemoveScamImageHash(str)
		if err != nil {
			return false, err
		}
		dbRemoved = removed
	}

	m.mu.Lock()
	kept := m.hashes[:0]
	cacheRemoved := false
	for _, kh := range m.hashes {
		if kh.str == str {
			cacheRemoved = true
			continue
		}
		kept = append(kept, kh)
	}
	m.hashes = kept
	m.mu.Unlock()

	return dbRemoved || cacheRemoved, nil
}

// hashCount returns the number of cached known-bad hashes.
func (m *Module) hashCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.hashes)
}
