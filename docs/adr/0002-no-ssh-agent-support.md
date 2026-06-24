# No ssh-agent support for decryption

dotsmith does not, and cannot, use ssh-agent to unlock passphrase-protected SSH keys; a passphrase-protected key is unlocked only by prompting on a TTY. This is recorded because users reasonably expect "my agent already has my key unlocked" to work, and a future contributor may try to add agent support — it is not a missing feature, it is impossible for age's scheme.

## Consequences

- age decrypts an `ssh-ed25519` stanza by converting the Ed25519 *private* key to X25519 and performing ECDH, and an `ssh-rsa` stanza via RSAES-OAEP *decryption*. ssh-agent only ever *signs* and never exposes private key material, so it physically cannot perform either operation. No amount of dotsmith code can bridge this.
- The only non-interactive decryption paths are therefore an **unencrypted** key (native or SSH) or running `compile` interactively so the passphrase can be entered. A passphrase-protected key with no TTY is a hard error by design.
- This limitation is documented in the README so users do not assume agent-backed decryption works.
