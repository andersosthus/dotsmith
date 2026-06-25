# Dotsmith

Dotfile management: subfile composition + Kustomize-style overlays + Stow-style
symlinks. This glossary pins the vocabulary that is easy to confuse — especially
around age decryption, where "key", "recipient", and "identity" are not
interchangeable.

## Language

### Encryption / decryption

**Identity**:
The secret key material dotsmith uses to *decrypt* an `.age` file. Dotsmith only
ever decrypts, so it only ever holds identities — never recipients.
_Avoid_: key, private key (too vague — say which kind of identity).

**Recipient**:
A *public* key an `.age` file was encrypted *to*. Recipients are chosen
out-of-band when the file is encrypted; dotsmith never holds or manages them, it
only reasons about which of its identities matches a file's recipients.
_Avoid_: public key, pubkey (when the age-format role is what matters).

**Native age identity**:
An identity in age's own key format (`AGE-SECRET-KEY-1…`), e.g. from
`age-keygen`. Configured explicitly via `age.identity_file`.
_Avoid_: age key, x25519 key.

**SSH identity**:
An identity backed by an existing SSH private key — `ssh-ed25519` or `ssh-rsa`
(RSA ≥ 2048-bit) only. Lets the same `.age` file be opened on different machines
by each machine's own SSH key, without per-machine config.
_Avoid_: ssh key (ambiguous between the public and private halves).

**Candidate identity set**:
The full collection of identities dotsmith offers to age for one decrypt — the
union of the configured native identity and all discovered SSH identities. age
matches a file's recipients against this set and uses whichever fits.
_Avoid_: keyring, identity list.

**Identity discovery**:
Building the candidate set from the machine automatically — primarily by reading
the SSH keys already present on the machine — rather than requiring each one to
be named in config.
_Avoid_: key scanning, auto-load.
