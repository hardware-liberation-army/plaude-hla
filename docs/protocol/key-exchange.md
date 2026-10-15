# BLE Key Exchange Findings

Analysis of the SN signature and RSA key exchange in the Plaud BLE protocol, clarifying what can and cannot be self-provisioned.

---

## Key Correction: RSA Public Key is Not Pre-Embedded

Previous documentation (REVERSE_ENGINEERING_SUMMARY.md, KEY_ACQUISITION.md) stated that the RSA public key was "previously sent to the device (or the device obtained it from the cloud)" during initial pairing. **This is incorrect.**

Evidence from the actual BLE protocol flow (`opendict_source/ble/PlaudCrypto.ts`) shows the RSA public key is sent **fresh during every BLE session**. The device explicitly requests it as part of the pre-handshake:

```
1. App  → Device: SN signature (cmd 0xFE10, chunked)
2. Device → App: "send your key" (cmd 0xFE11)
3. App  → Device: RSA public key PEM as raw bytes (cmd 0xFE12, chunked)
4. Device → App: RSA-encrypted session material (cmd 0xFE12, chunked)
```

The device has **no pre-stored RSA public key**. It encrypts the ChaCha20-Poly1305 session keys (E, F, G) with whatever public key is presented in step 3.

## Implications

### RSA Key Pair Can Be Self-Generated

Since the device accepts any RSA public key presented during the session, we can:

1. Generate a fresh RSA-2048 key pair locally
2. Present the public key during BLE pre-handshake (step 3)
3. Decrypt the session material with our private key

**No need to extract an RSA key from the phone app.** The `rsa_private_key_pem` config value can be a locally-generated key.

### SN Signature Cannot Be Self-Generated

The SN signature is the **authorization gate**. It is:

- A 256-byte opaque blob (RSA-2048 ciphertext)
- Signed by PLAUD's server-side private key
- Verified by the device using PLAUD's public key **baked into firmware**
- Specific to a device serial number and bound to a user account
- Obtained from `POST /device/sn-sign` with a valid API token

Without a valid SN signature, the device will not proceed past step 1. It will not send the 0xFE11 "send your key" response, and the key exchange never begins.

**We cannot bypass `/device/sn-sign`** without:

- PLAUD's server-side signing key (not feasible)
- Modified device firmware that skips SN verification
- A vulnerability in the signature verification logic

### SN Signature Expiry

**Unknown.** The SN signature may be:

- Long-lived (valid indefinitely until device is rebound)
- Time-based (expires after some period)
- Session-based (one-time use)

The signature in `~/.config/aplaud/config.yaml` was fetched on 2026-05-07. Empirical testing is needed — try connecting with the same signature days/weeks later and see if the device accepts it.

If long-lived, the cloud dependency becomes a **one-time setup step**: fetch the SN signature once, then operate indefinitely without PLAUD's servers.

## What Still Requires PLAUD's Cloud

| Item | Cloud Required? | Notes |
|------|----------------|-------|
| SN signature | **Yes** | Server-signed authorization blob; cannot be self-generated |
| API token | **Yes** | Needed to call `/device/sn-sign`; email/password login available |
| E2EE decryption key | **Yes** | Fetched from `/e2ee/gen_key`; needed to decrypt synced recordings |
| RSA key pair (BLE transport) | **No** | Self-generated, sent fresh each session |
| Device SN | **No** | Obtainable via BLE scan or physical inspection |

## Complete Self-Setup Flow (After Unbinding Device)

If the device is unbound from the mobile app:

1. **Generate RSA-2048 key pair locally** — no cloud needed
2. **Authenticate with PLaud cloud** — email/password login via `/auth/access-token`
3. **Fetch SN signature** — `POST /device/sn-sign` with API token + device SN (optionally include new public key, though the device receives it directly during BLE session anyway)
4. **Fetch E2EE key** — `POST /e2ee/gen_key` with API token
5. **Connect via BLE** — send SN signature, send our RSA public key, decrypt session material, proceed with encrypted handshake
6. **Done** — full device control without the phone app

Steps 2-4 are the only cloud interactions. Steps 1 and 5 are entirely local.

## Files Corrected

- `REVERSE_ENGINEERING_SUMMARY.md` — Fixed RSA public key description (was "previously sent to device", now "sent fresh each session")
- `KEY_ACQUISITION.md` — Updated `rsa_private_key_pem` section to reflect that any RSA key pair can be used
