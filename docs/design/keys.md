# The encryption key: choosing it, storing it, backing it up

manualbox encrypts a narrow set of fields — serial numbers, receipts, purchase
prices — because the realistic threat is a copy of the data directory escaping.
See [privacy.md](privacy.md) for why that set and not more.

This page answers the three questions a user actually has: *what is my key, where
is it, and what happens if I lose it?*

## Two ways to unlock, and you want both

A memorable passphrase and an unattended restart are in direct conflict. A
household server should come back after a power cut without anyone connecting to
type anything — but a key that lives in a file is one nobody can remember, and it
is worthless for restoring a backup onto a different machine.

So neither encrypts the data. A random data key does, and that key is *wrapped*
separately by each method:

```
                    ┌─ wrapped by your passphrase ──────┐
   random data key ─┤                                    ├─ either one opens it
                    └─ wrapped by the operational key ──┘
```

| | Passphrase | Operational key |
|---|---|---|
| Who picks it | **you do** — something you will remember | generated for you, 32 random bytes |
| Where it lives | your head, and your password manager | an environment variable or Docker secret |
| Unattended restart | no — someone must type it | **yes** |
| Restores a backup on a new machine | **yes** | only if you also kept the key |
| Unlock cost | ~200 ms (deliberately slow) | instant |

Both are optional and you can have either or both. The recommended setup is
**both**: the operational key does the day-to-day work, and the passphrase is your
recovery path for the day the machine is gone.

Adding a passphrase later, or rotating the operational key, rewraps a 32-byte
secret. It never re-encrypts your data, so it is instant and safe to do at any
time.

## Where everything is stored

This is the part worth reading twice, because it determines what you need to back
up.

| | Location | In backups? |
|---|---|---|
| **The wrapped key** — `keyring.json` | inside the data directory | **yes, and that is fine.** Without a passphrase or the operational key it is inert: 32 bytes of noise. |
| **The operational key** | `MANUALBOX_DATA_KEY`, a Docker secret, or a file *outside* the data directory | **no** — keeping it beside the data would defeat the entire point |
| **Your passphrase** | your memory and your password manager | never stored anywhere by manualbox |

The operational key is deliberately *not* in the data directory. The threat being
defended against is someone obtaining a copy of that directory; storing the key
inside it would hand them the key along with the data.

## Backing up

> **Back up the data directory. To restore it you will need either your
> passphrase, or the operational key. Keep at least one of them somewhere other
> than the machine.**

manualbox prints a **recovery kit** when encryption is first enabled — a single
page containing the instance name, the operational key, and these instructions —
and it can print it again at any time. Save it to your password manager or print
it. It is the only copy.

### Test the backup, do not assume it

An untested backup is not a backup, and discovering a misremembered passphrase
during a real restore is discovering it far too late. So:

```sh
manualbox keys verify          # prompts for a passphrase and says whether it opens the data
```

This changes nothing. It exists purely so you can find out today rather than in an
emergency.

## The command surface

```sh
manualbox keys status          # which unlock methods exist, and where each one is kept
manualbox keys add-passphrase  # gain a recovery path without re-encrypting anything
manualbox keys rotate          # issue a new operational key, keeping the old wraps working
manualbox keys verify          # prove a secret still opens the data
manualbox keys recovery-kit    # reprint the page to save or keep on paper
manualbox keys remove <id>     # drop an unlock method (refuses to remove the last one)
```

`keys remove` refusing the final wrap is not politeness. A keyring with no wraps is
data nobody can ever read again, and there is no way to warn someone after the
fact.

## When it is set up, and what happens if you decline

Not during first-run setup. Asking someone to make a decision about encryption keys
before they have seen the application is a good way to have them choose badly, and
most of what manualbox stores does not need it.

The question appears the first time you store something that actually warrants it —
adding a serial number, or uploading a receipt — with the reason visible in
context:

> Serial numbers and receipts are the most sensitive things manualbox will hold. It
> can encrypt them so a copy of your data folder is not enough to read them.
> Encrypt them? · *Not now* · *Yes, set up a key*

**Declining is a legitimate, permanent choice, not a nag.** Everything works
without encryption; the fields are simply stored in the clear, protected by your
login and by whatever disk encryption the host already has. For many households
that is the right answer, and full-disk encryption on the host covers the same
threat with none of the key management.

And the trade must be stated before anyone opts in, not after:

> **If you lose both your passphrase and the operational key, these fields cannot
> be recovered.** Not by you, and not by anyone else. That is what encryption
> means.

A household tool that silently destroys someone's warranty records because they
rebuilt a container without the secret would be worse than not encrypting at all.
That is why it is opt-in, why the recovery kit is printed up front, and why
`verify` exists.

## Implementation notes

`internal/keyring`, with 28 tests covering the properties that cannot be eyeballed:
either secret opens the same data, the on-disk file contains no secret verbatim,
data encrypted before a passphrase existed still opens with it afterwards, the same
value never encrypts to the same ciphertext twice, tampering is rejected rather
than returning altered plaintext, and the search index survives a reopen so
yesterday's rows stay findable.

- Data key: 32 random bytes, AES-256-GCM, nonce prepended to each value.
- Passphrase wrap: argon2id, 256 MiB / 4 passes / 4 lanes, per-wrap salt and
  parameters so the cost can be raised later without invalidating old passphrases.
- Operational wrap: HKDF-SHA256 from the stored key, no KDF cost, instant boot.
- Search index: HMAC-SHA256 under an HKDF subkey — not the encryption key itself —
  so encrypted serial numbers stay findable by exact match. Substring search is
  lost, which is acceptable: nobody types half a serial number.
