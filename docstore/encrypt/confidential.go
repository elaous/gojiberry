package encrypt

import "context"

// ConfidentialCheck is the hook a saver asks before a document is made end to end protected
// (saver.Saver.ConfidentialCheck): it refuses with ErrNeedSecondRecipient unless one of the
// owner's own keys is not a passkey (CheckRecoverableOwner), so that losing the passkey does
// not lose the document. owners are the owner's keys, the ones every document is encrypted to
// (StoreKeyring.Owners); the keys the document is shared with are read from sharing's signed
// list and, as CheckRecoverableOwner says, do not count: a recovery key has to be one the
// owner controls. A nil sharing means no one else's keys are listed; a list that does not
// verify is an error (fail closed). The saver does not import this package: pass the result
// as the hook.
func ConfidentialCheck(sharing *Sharing, owners ...Recipient) func(ctx context.Context, target, id string) error {
	return func(ctx context.Context, _, id string) error {
		var shared []Recipient
		if sharing != nil {
			list, err := sharing.List(ctx, id)
			if err != nil {
				return err
			}
			for _, s := range list {
				shared = append(shared, Recipient{Label: s.Label, Type: s.Type, Fingerprint: s.Fingerprint, Text: s.Key, Hint: s.Hint})
			}
		}
		return CheckRecoverableOwner(owners, shared)
	}
}
