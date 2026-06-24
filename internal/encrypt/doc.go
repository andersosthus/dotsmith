// Package encrypt wraps filippo.io/age for transparent decryption of dotfiles
// during compilation.
//
// dotsmith only ever decrypts. The decryption identity is a candidate identity
// set assembled once per run by Resolve from the native age identity, any
// explicitly configured identity paths, and SSH keys discovered in ~/.ssh/. age
// matches a file's recipients against the whole set, so a single committed .age
// file encrypted to several machines' SSH public keys opens on each machine with
// that machine's own key. Decrypt and DecryptFile consume the resolved
// IdentitySet so a passphrase-protected key is unlocked at most once per run.
package encrypt
