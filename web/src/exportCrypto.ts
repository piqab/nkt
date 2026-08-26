/**
 * Password-based encryption for hub export files — byte-for-byte the same
 * envelope internal/secretbox.EncryptWithPassword produces in Go (magic ||
 * salt || iterations(uint32 BE) || nonce || ciphertext+tag): PBKDF2-HMAC-
 * SHA256 deriving an AES-256-GCM key, both available natively via
 * SubtleCrypto, no extra crypto library in the bundle. A file this code
 * encrypts decrypts via `nkt hub import`; a file `nkt hub delete`'s own
 * export step (or another hub's export button) encrypts decrypts here —
 * same format either direction.
 */
import i18n from './i18n'

const MAGIC = new Uint8Array([0x4e, 0x4b, 0x54, 0x31]) // "NKT1"
const SALT_SIZE = 16
// Matches internal/secretbox.defaultIterations (OWASP's 2023 minimum for
// PBKDF2-HMAC-SHA256) — encryptWithPassword always writes this; the
// envelope carries its own iteration count too, so decryptWithPassword
// never assumes it (an older/newer writer's value still decrypts).
const ITERATIONS = 600_000
const NONCE_SIZE = 12

/** True when data starts with the password-envelope's magic bytes — enough
 * to route an uploaded/pasted file without needing (or trying) a password
 * first. Plain export JSON never happens to start with these four bytes. */
export function isPasswordEncrypted(data: Uint8Array): boolean {
  return data.length >= MAGIC.length && MAGIC.every((b, i) => data[i] === b)
}

function readUint32BE(data: Uint8Array, offset: number): number {
  return ((data[offset] << 24) | (data[offset + 1] << 16) | (data[offset + 2] << 8) | data[offset + 3]) >>> 0
}

function writeUint32BE(n: number): Uint8Array {
  return new Uint8Array([(n >>> 24) & 0xff, (n >>> 16) & 0xff, (n >>> 8) & 0xff, n & 0xff])
}

async function deriveAesKey(
  password: string,
  salt: Uint8Array,
  iterations: number,
  usage: KeyUsage[],
): Promise<CryptoKey> {
  const passwordKey = await crypto.subtle.importKey('raw', new TextEncoder().encode(password), 'PBKDF2', false, [
    'deriveKey',
  ])
  return crypto.subtle.deriveKey(
    { name: 'PBKDF2', salt: salt as BufferSource, iterations, hash: 'SHA-256' },
    passwordKey,
    { name: 'AES-GCM', length: 256 },
    false,
    usage,
  )
}

export async function encryptWithPassword(password: string, plaintext: Uint8Array): Promise<Uint8Array> {
  const salt = crypto.getRandomValues(new Uint8Array(SALT_SIZE))
  const key = await deriveAesKey(password, salt, ITERATIONS, ['encrypt'])
  const nonce = crypto.getRandomValues(new Uint8Array(NONCE_SIZE))
  const ciphertext = new Uint8Array(
    await crypto.subtle.encrypt({ name: 'AES-GCM', iv: nonce as BufferSource }, key, plaintext as BufferSource),
  )

  const iterBytes = writeUint32BE(ITERATIONS)
  const out = new Uint8Array(MAGIC.length + salt.length + iterBytes.length + nonce.length + ciphertext.length)
  let offset = 0
  out.set(MAGIC, offset)
  offset += MAGIC.length
  out.set(salt, offset)
  offset += salt.length
  out.set(iterBytes, offset)
  offset += iterBytes.length
  out.set(nonce, offset)
  offset += nonce.length
  out.set(ciphertext, offset)
  return out
}

export async function decryptWithPassword(password: string, envelope: Uint8Array): Promise<Uint8Array> {
  if (!isPasswordEncrypted(envelope)) {
    throw new Error(i18n.t('hosts.notPasswordEncrypted'))
  }
  const headerSize = MAGIC.length + SALT_SIZE + 4
  if (envelope.length < headerSize + NONCE_SIZE) {
    throw new Error(i18n.t('hosts.corruptedFileTooShort'))
  }
  let offset = MAGIC.length
  const salt = envelope.slice(offset, offset + SALT_SIZE)
  offset += SALT_SIZE
  const iterations = readUint32BE(envelope, offset)
  offset += 4
  const nonce = envelope.slice(offset, offset + NONCE_SIZE)
  offset += NONCE_SIZE
  const ciphertext = envelope.slice(offset)

  const key = await deriveAesKey(password, salt, iterations, ['decrypt'])
  try {
    const plaintext = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: nonce as BufferSource }, key, ciphertext as BufferSource)
    return new Uint8Array(plaintext)
  } catch {
    throw new Error(i18n.t('hosts.wrongPasswordOrCorrupted'))
  }
}
