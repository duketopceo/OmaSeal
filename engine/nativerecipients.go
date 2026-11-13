package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"filippo.io/age"
)

// Additional machine recipients and the read-only replica guard. Design:
// docs/plans/2026-11-12-001-feat-native-multi-recipient-replica-plan.md
// and the addendum in docs/design/native-store.md.
//
// Layout addition under $XDG_STATE_HOME/omaseal/native/:
//
//	recipients.json — committed extra recipients (name + age1 public key).
//	                  Written only by the gated rotation path. Set encrypts
//	                  to identity.pub plus this snapshot, never to a raw
//	                  config.json edit.

const nativeRecipientsFile = "recipients.json"

var (
	errReplicaReadOnly   = errors.New("store is a read-only replica")
	errAlreadyRecipient  = errors.New("already a recipient")
	errRecipientNotFound = errors.New("recipient not found")
	errLocalRecipient    = errors.New("cannot add or remove the local identity")
	errRecipientName     = errors.New("invalid recipient name")
)

// recipientNameRe: starts with alphanumeric, then alphanumerics, dots,
// underscores or hyphens, up to 64 characters. A name is a handle for
// humans and the audit log, not a security boundary.
var recipientNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func replicaError() error {
	return newError("replica_readonly",
		"this instance is a read-only replica — write on the primary and sync",
		errReplicaReadOnly)
}

func (s *nativeStore) refuseReplicaWrite() error {
	if s.replica {
		return replicaError()
	}
	return nil
}

// recipientsDoc is the on-disk committed snapshot. The local identity is
// never listed — it is always a recipient via identity.pub.
type recipientsDoc struct {
	Recipients []namedRecipient `json:"recipients"`
}

func (s *nativeStore) recipientsPath() string {
	return filepath.Join(s.dir, nativeRecipientsFile)
}

// loadExtra reads the committed snapshot into s.extra. A missing file is
// an empty set (stores that predate this unit). A present but unreadable
// or invalid file fails closed — Set must not silently drop recipients.
func (s *nativeStore) loadExtra() error {
	raw, err := os.ReadFile(s.recipientsPath())
	if errors.Is(err, os.ErrNotExist) {
		s.extra = nil
		return nil
	}
	if err != nil {
		return keyringError(err)
	}
	var doc recipientsDoc
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return keyringError(fmt.Errorf("parse %s: %w", nativeRecipientsFile, err))
		}
	}
	for i, r := range doc.Recipients {
		if _, err := age.ParseX25519Recipient(strings.TrimSpace(r.Pubkey)); err != nil {
			return keyringError(fmt.Errorf("%s entry %d (%s): %w", nativeRecipientsFile, i, r.Name, err))
		}
		if r.Name == "" {
			return keyringError(fmt.Errorf("%s entry %d: missing name", nativeRecipientsFile, i))
		}
	}
	s.extra = doc.Recipients
	return nil
}

func (s *nativeStore) writeCommitted(extra []namedRecipient) error {
	if extra == nil {
		extra = []namedRecipient{}
	}
	raw, err := json.Marshal(recipientsDoc{Recipients: extra})
	if err != nil {
		return keyringError(err)
	}
	return atomicWriteFile(s.recipientsPath(), raw, 0600)
}

// encryptRecipients is the Set/rotation recipient set: local pub plus every
// committed extra. Caller must have ensureInit'd (s.pub) and loadExtra'd.
func (s *nativeStore) encryptRecipients() ([]age.Recipient, error) {
	if s.pub == nil {
		return nil, errors.New("store public key not loaded")
	}
	recs := make([]age.Recipient, 0, 1+len(s.extra))
	recs = append(recs, s.pub)
	for _, r := range s.extra {
		p, err := age.ParseX25519Recipient(strings.TrimSpace(r.Pubkey))
		if err != nil {
			return nil, keyringError(fmt.Errorf("recipient %s: %w", r.Name, err))
		}
		recs = append(recs, p)
	}
	return recs, nil
}

func (s *nativeStore) encryptEnvelope(env nativeEnvelope) (string, error) {
	plain, err := json.Marshal(env)
	if err != nil {
		return "", keyringError(err)
	}
	recs, err := s.encryptRecipients()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recs...)
	if err != nil {
		return "", keyringError(err)
	}
	if _, err := w.Write(plain); err != nil {
		return "", keyringError(err)
	}
	if err := w.Close(); err != nil {
		return "", keyringError(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// decryptEnvelope unwraps one item and checks the name binding (I5).
// Caller must have the identity in memory.
func (s *nativeStore) decryptEnvelope(service, account string, it nativeItem) (string, error) {
	id := s.identity.Load()
	if id == nil {
		return "", newError("locked", "omaseal agent unlock (or run omaseal get in a terminal)", errNativeLocked)
	}
	raw, err := base64.StdEncoding.DecodeString(it.CT)
	if err != nil {
		return "", keyringError(err)
	}
	src, err := age.Decrypt(bytes.NewReader(raw), id)
	if err != nil {
		return "", keyringError(fmt.Errorf("decrypt: %w", err))
	}
	plain, err := io.ReadAll(src)
	if err != nil {
		return "", keyringError(err)
	}
	var env nativeEnvelope
	if err := json.Unmarshal(plain, &env); err != nil {
		return "", keyringError(err)
	}
	if env.Svc != service || env.Acct != account {
		return "", newError("tamper", "store integrity check failed — see omaseal doctor", errNativeTamper)
	}
	return env.Value, nil
}

func validateRecipientName(name string) error {
	if !recipientNameRe.MatchString(name) {
		return newError("invalid_recipient",
			"use a name of 1–64 characters starting with a letter or digit, then letters, digits, dots, underscores or hyphens",
			fmt.Errorf("%w: %q", errRecipientName, name))
	}
	return nil
}

func validateRecipientPubkey(pubkey string) (*age.X25519Recipient, error) {
	r, err := age.ParseX25519Recipient(strings.TrimSpace(pubkey))
	if err != nil {
		return nil, newError("invalid_recipient",
			"pass an age X25519 public key (age1…), from `omaseal recipients local` on the other machine",
			err)
	}
	return r, nil
}

func (s *nativeStore) localPubString() (string, error) {
	if err := s.ensureInit(); err != nil {
		return "", err
	}
	if s.pub == nil {
		return "", errors.New("store public key not loaded")
	}
	return s.pub.String(), nil
}

// addRecipient appends a machine recipient and re-encrypts every item to
// the new set. The passphrase-wrapped identity is not touched. Refuses on
// a replica. Presence gating is the CLI's job.
func (s *nativeStore) addRecipient(name, pubkey string) error {
	if err := s.refuseReplicaWrite(); err != nil {
		return err
	}
	if err := validateRecipientName(name); err != nil {
		return err
	}
	parsed, err := validateRecipientPubkey(pubkey)
	if err != nil {
		return err
	}
	pubkey = parsed.String()
	if err := s.ensureInit(); err != nil {
		return err
	}
	if err := s.ensureIdentity(); err != nil {
		return err
	}
	if err := s.loadExtra(); err != nil {
		return err
	}
	local, err := s.localPubString()
	if err != nil {
		return err
	}
	if pubkey == local {
		return newError("invalid_recipient",
			"that key is this machine's local identity; it is already a recipient",
			errLocalRecipient)
	}
	for _, r := range s.extra {
		if r.Pubkey == pubkey || r.Name == name {
			return newError("already_recipient",
				"omaseal recipients list",
				fmt.Errorf("%w: %s", errAlreadyRecipient, r.Name))
		}
	}
	next := append(append([]namedRecipient{}, s.extra...), namedRecipient{Name: name, Pubkey: pubkey})
	return s.rotateTo(next)
}

// removeRecipient drops a recipient by name or public key and re-encrypts
// every item so the removed key can no longer decrypt. The local identity
// cannot be removed.
func (s *nativeStore) removeRecipient(id string) (namedRecipient, error) {
	var zero namedRecipient
	if err := s.refuseReplicaWrite(); err != nil {
		return zero, err
	}
	if err := s.ensureInit(); err != nil {
		return zero, err
	}
	if err := s.ensureIdentity(); err != nil {
		return zero, err
	}
	if err := s.loadExtra(); err != nil {
		return zero, err
	}
	local, err := s.localPubString()
	if err != nil {
		return zero, err
	}
	if id == local || id == "local" {
		return zero, newError("invalid_recipient",
			"the local identity is always a recipient and cannot be removed",
			errLocalRecipient)
	}
	idx := -1
	var removed namedRecipient
	for i, r := range s.extra {
		if r.Name == id || r.Pubkey == id {
			idx = i
			removed = r
			break
		}
	}
	if idx < 0 {
		return zero, newError("not_found", "omaseal recipients list",
			fmt.Errorf("%w: %s", errRecipientNotFound, id))
	}
	next := append([]namedRecipient{}, s.extra[:idx]...)
	next = append(next, s.extra[idx+1:]...)
	if err := s.rotateTo(next); err != nil {
		return zero, err
	}
	return removed, nil
}

// rotateTo re-encrypts every item to local pub + next, then writes the
// committed snapshot. On any decrypt failure the store file is not
// written (updateErr only writes after fn returns nil) and s.extra is
// reloaded from the existing snapshot.
func (s *nativeStore) rotateTo(next []namedRecipient) error {
	if err := s.refuseReplicaWrite(); err != nil {
		return err
	}
	if err := s.ensureIdentity(); err != nil {
		return err
	}
	prev := s.extra
	err := s.updateErr(func(d *nativeDoc) error {
		type decoded struct {
			key  string
			svc  string
			acct string
			it   nativeItem
			val  string
		}
		items := make([]decoded, 0, len(d.Items))
		for k, it := range d.Items {
			rawSvc, rawAcct, ok := strings.Cut(k, "/")
			if !ok || rawSvc == "" || rawAcct == "" {
				return keyringError(fmt.Errorf("rotate: malformed store key %q", k))
			}
			svc, acct := unescKeyPart(rawSvc), unescKeyPart(rawAcct)
			val, err := s.decryptEnvelope(svc, acct, it)
			if err != nil {
				return fmt.Errorf("rotate: decrypt %s/%s: %w", svc, acct, err)
			}
			items = append(items, decoded{key: k, svc: svc, acct: acct, it: it, val: val})
		}
		s.extra = next
		for _, it := range items {
			ct, err := s.encryptEnvelope(nativeEnvelope{Svc: it.svc, Acct: it.acct, Value: it.val})
			if err != nil {
				s.extra = prev
				return err
			}
			rec := it.it
			rec.CT = ct
			rec.Updated = s.now().Unix()
			d.Items[it.key] = rec
		}
		// Commit the snapshot before the store write so a failed store
		// write leaves committed ahead (new Sets include the new set)
		// rather than store ahead of committed. add/remove of a key
		// already in the snapshot is rejected, which is the documented
		// crash window; both files are 0600 atomic renames.
		if err := s.writeCommitted(next); err != nil {
			s.extra = prev
			return err
		}
		return nil
	})
	if err != nil {
		s.extra = prev
		return err
	}
	return nil
}

func (s *nativeStore) committedRecipients() []namedRecipient {
	out := make([]namedRecipient, len(s.extra))
	copy(out, s.extra)
	return out
}
