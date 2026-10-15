package encrypt

import (
	"context"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

var _ KeyDirectory = (*AdapterDirectory)(nil)

// DirectoryService is the docstore.DirectoryService over d, for docstore.Server.WithDirectory:
// the routes call it after checking the session. It maps the directory's refusals to the
// errors the server answers with.
func DirectoryService(d *AdapterDirectory) docstore.DirectoryService { return dirService{d} }

type dirService struct{ d *AdapterDirectory }

// refusal wraps the directory's own refusals as docstore.ErrDirectoryRefused (403); the
// docstore errors pass through.
func refusal(err error) error {
	switch {
	case errors.Is(err, docstore.ErrDirectoryBadAddress):
		return err
	case errors.Is(err, errEnrollmentCorrupt):
		return fmt.Errorf("%w: %v", docstore.ErrDirectoryRefused, err)
	case errors.Is(err, ErrNotPublishable), errors.Is(err, ErrTooManyKeys), errors.Is(err, ErrDirectoryOff):
		return fmt.Errorf("%w: %v", docstore.ErrDirectoryRefused, err)
	}
	return err
}

func toDirectoryKey(k PublishedKey) docstore.DirectoryKey {
	return docstore.DirectoryKey{Key: k.Text, Label: k.Label, Type: k.Type, Fingerprint: k.Fingerprint, Verified: k.Verified,
		Held: k.Held, Attested: k.Attested, Attestation: k.Attestation, Seq: k.Seq, Kid: k.Kid, Revoked: k.Revoked, RevokedAt: k.RevokedAt,
		OwnerEnrolled: k.OwnerEnrolled, OwnerSigned: k.OwnerSigned, OwnerResetPending: k.OwnerResetPending, Owner: k.Owner}
}

func (s dirService) Challenge(ctx context.Context, user auth.User, key string) (docstore.DirectoryChallenge, error) {
	c, err := s.d.Challenge(ctx, user, key)
	if errors.Is(err, errBadPublicKey) {
		return docstore.DirectoryChallenge{}, fmt.Errorf("%w: %v", docstore.ErrDirectoryBadKey, err)
	}
	return c, refusal(err)
}

func (s dirService) Publish(ctx context.Context, user auth.User, req docstore.DirectoryPublish) (docstore.DirectoryKey, error) {
	r, err := parsePublic(req.Key)
	if err != nil {
		return docstore.DirectoryKey{}, fmt.Errorf("%w: %v", docstore.ErrDirectoryBadKey, err)
	}
	r.Label = req.Label
	opt := PublishOptions{Attestation: req.Attestation, Owner: req.Owner}
	if req.Proof != nil {
		opt.Challenge, opt.Nonce = req.Proof.Token, req.Proof.Nonce
	}
	k, err := s.d.PublishWith(ctx, user, r, opt)
	if err != nil {
		return docstore.DirectoryKey{}, refusal(err)
	}
	return toDirectoryKey(k), nil
}

func (s dirService) Revoke(ctx context.Context, user auth.User, fingerprint string, owner *docstore.DirectoryOwnerSig) (docstore.DirectoryKey, error) {
	k, err := s.d.Revoke(ctx, user, fingerprint, owner)
	if err != nil {
		return docstore.DirectoryKey{}, refusal(err)
	}
	return toDirectoryKey(k), nil
}

func (s dirService) Lookup(ctx context.Context, email string, includeRevoked bool) ([]docstore.DirectoryKey, error) {
	keys, err := s.d.LookupWith(ctx, email, LookupOptions{IncludeRevoked: includeRevoked})
	if err != nil {
		return nil, err
	}
	out := make([]docstore.DirectoryKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, toDirectoryKey(k))
	}
	return out, nil
}

func (s dirService) Log(ctx context.Context, since int64, limit int) (docstore.DirectoryLog, error) {
	return s.d.ReadLog(ctx, since, limit)
}

// LogFor implements docstore.DirectoryLogFilterer.
func (s dirService) LogFor(ctx context.Context, addresses []string, since int64, scan, limit int) (docstore.DirectoryLog, error) {
	return s.d.LogFor(ctx, addresses, since, scan, limit)
}

// OperatorResetEnrollment implements docstore.DirectoryOperator.
func (s dirService) OperatorResetEnrollment(ctx context.Context, pseudonym string) (docstore.DirectoryEnrollment, error) {
	return s.d.OperatorResetEnrollment(ctx, pseudonym)
}

// UnmappedAddresses implements docstore.DirectoryUnmappedLister.
func (s dirService) UnmappedAddresses(ctx context.Context) (docstore.DirectoryUnmapped, error) {
	return s.d.UnmappedAddresses(ctx)
}

func (s dirService) Head(ctx context.Context) (docstore.DirectoryHead, error) { return s.d.Head(ctx) }

func (s dirService) LogAddress(_ context.Context, user auth.User) (docstore.DirectoryLogAddress, error) {
	if s.d.Signer == nil {
		return docstore.DirectoryLogAddress{}, refusal(ErrDirectoryOff)
	}
	if err := s.d.checkUser(user); err != nil {
		return docstore.DirectoryLogAddress{}, refusal(err)
	}
	all := s.d.LogAddressesFor(user.Email)
	return docstore.DirectoryLogAddress{Address: all[0], Previous: all[1:]}, nil
}

func (s dirService) Enrollment(ctx context.Context, email string) (docstore.DirectoryEnrollment, error) {
	e, err := s.d.Enrollment(ctx, email)
	return e, refusal(err)
}

func (s dirService) EnrollChallenge(ctx context.Context, user auth.User) (docstore.DirectoryEnrollChallenge, error) {
	c, err := s.d.EnrollChallenge(ctx, user)
	return c, refusal(err)
}

func (s dirService) Enroll(ctx context.Context, user auth.User, req docstore.DirectoryEnroll) (docstore.DirectoryEnrollment, error) {
	e, err := s.d.Enroll(ctx, user, req)
	return e, refusal(err)
}

func (s dirService) RequestReset(ctx context.Context, user auth.User) (docstore.DirectoryEnrollment, error) {
	e, err := s.d.RequestReset(ctx, user)
	return e, refusal(err)
}

func (s dirService) CancelReset(ctx context.Context, user auth.User, owner *docstore.DirectoryOwnerSig) (docstore.DirectoryEnrollment, error) {
	e, err := s.d.CancelReset(ctx, user, owner)
	return e, refusal(err)
}

// DirectoryHealth implements docstore.DirectoryHealther: the census of the last Startup.
func (s dirService) DirectoryHealth() docstore.DirectoryHealth { return s.d.Health() }

// ParsePublicKey reads a public key as the directory takes it: "age1..." (an age, recovery or
// passkey key) or an "ssh-ed25519 ..."/"ssh-rsa ..." line. It is how a client gets the type and
// the fingerprint a publish names.
func ParsePublicKey(text string) (Recipient, error) { return parsePublic(text) }

// AddCosignature implements docstore.DirectoryWitnessing.
func (s dirService) AddCosignature(ctx context.Context, c docstore.DirectoryCosignature) error {
	return refusal(s.d.AddCosignature(ctx, c))
}

// Cosigned implements docstore.DirectoryWitnessing.
func (s dirService) Cosigned(ctx context.Context) ([]docstore.DirectoryCosigned, error) {
	c, err := s.d.Cosigned(ctx)
	return c, refusal(err)
}
