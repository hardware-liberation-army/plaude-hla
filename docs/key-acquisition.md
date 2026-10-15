# Key Acquisition & Setup Wizard Feasibility

Analysis of every config key the `aplaud` Go app needs, where each comes from, whether it differs per user or expires, and whether a setup wizard (`aplaud setup`) could obtain it programmatically.

---

## Required Keys

### 1. `api_token` (JWT)

- **Scope**: Per-user
- **Expires**: Yes. The JWT has an `exp` claim. Observed lifetimes range from ~30 days (SDK-derived tokens) to ~2 years (web-extracted tokens).
- **How to obtain programmatically**:
  - **Email/password login**: `POST https://api.plaud.ai/auth/access-token` with `username=<email>&password=<password>` (form-encoded). The API has regional endpoints (`api.plaud.ai` for US, `api-euc1.plaud.ai` for EU). Region auto-detection works: if the response has `status: -302`, follow `data.domains.api` to the correct region.
  - **SDK token exchange**: Two-step OAuth via `platform.plaud.cn` — get SDK token with `Basic base64(app_key:app_secret)`, then exchange for API token. Requires SDK app key/secret (hardcoded in the SDK, not user-specific).
- **Existing code**:
  - Go: `aplaud/internal/auth/sdk.go` — `GetAPIToken()` implements SDK exchange, but has a **bug at line 32** (`appSecret: cachePath` should be `appSecret: appSecret`) and is **never called from any CLI command**.
  - TypeScript: `opendict_source/plaud-sdk/PlaudAPI.ts` — `login()` implements email/password login with region fallback.
  - Python: `sdk_auth.py` — `full_sdk_auth()` implements SDK exchange with JSON cache.
- **Setup wizard approach**: Prompt user for Plaud account email and password, call `/auth/access-token` with region auto-detection. Cache the resulting token. This is the cleanest user experience — no SDK credentials needed.

### 2. `sn_signature_b64` (SN Signature)

- **Scope**: Per-device AND per-user. Specific to a device serial number, but requires a valid user API token to fetch.
- **Expires**: Unknown. Possibly long-lived, possibly tied to device binding. The REVERSE_ENGINEERING_SUMMARY notes it "may expire and need re-fetching."
- **How to obtain programmatically**: `POST https://api.plaud.ai/device/sn-sign` with `Authorization: bearer <api_token>` and body `{"sns": ["<device_sn>"]}`. Optionally includes `public_key_base64` (the user's RSA public key, PEM with headers stripped and base64'd). Returns `{"data": {"signature": {"<sn>": "<base64 blob>"}}}`.
- **Existing code**:
  - TypeScript only: `opendict_source/plaud-sdk/PlaudAPI.ts` — `getSnSignature()` (lines 80-103).
  - **No Go or Python implementation exists.**
- **Setup wizard approach**: After obtaining the API token and discovering the device SN (via BLE scan or cloud API), fetch the SN signature automatically.

### 3. `rsa_private_key_pem` (BLE Transport RSA Key)

- **Scope**: Per-session. Can be any RSA-2048 key pair — not tied to a specific user account or device.
- **Expires**: N/A. Generated locally and used for the duration of a BLE session.
- **How to obtain**: **Self-generated.** The RSA public key is sent to the device fresh during each BLE session as part of the pre-handshake flow (cmd 0xFE12, after the SN signature). The device has no pre-stored RSA public key — it encrypts session material (ChaCha20-Poly1305 keys) with whatever public key is presented. This was confirmed by examining the protocol in `opendict_source/ble/PlaudCrypto.ts`.
- **Previous understanding (CORRECTED)**: Earlier documentation stated "The private key is stored on the PLAUD servers and sent to the app. The public key is sent to the BLE device (either directly during pairing or via the cloud)." This was incorrect — the public key is not pre-embedded on the device. It is transmitted during each connection.
- **Existing code**: No code in the codebase generates a new RSA key pair (yet). This should be added to the setup wizard.
- **Setup wizard approach**: Generate a fresh RSA-2048 key pair locally during setup. No extraction from the phone app needed. See `BLE_KEY_EXCHANGE_FINDINGS.md` for full analysis.

### 4. `e2ee_rsa_key_path` (E2EE File Decryption Key)

- **Scope**: Per-user. Tied to the user account.
- **Expires**: The endpoint is named `gen_key`, suggesting possible rotation. In practice the same key is returned consistently for the same account, but this is not guaranteed. **Critical**: if a new key IS generated, recordings encrypted with the old key become undecryptable without a backup.
- **How to obtain programmatically**: `POST https://api-apse1.plaud.ai/e2ee/gen_key` with `Authorization: Bearer <api_token>` and empty JSON body `{}`. Returns `{"data": {"private_key_base64": "<base64 PEM>"}}`.
- **Existing code**:
  - Go: `aplaud/internal/auth/sdk.go` — `FetchE2EERSKey()` (lines 150-183). Exists but **no CLI command invokes it**.
  - Python: `plaud_decrypt.py` — `--fetch-key` flag. Backs up old key to `.plaud_rsa_key.bak.{timestamp}` before overwriting.
- **Setup wizard approach**: After obtaining the API token, fetch the E2EE key and save to `~/.config/aplaud/plaud_rsa_key`. If a key already exists, back it up first.

### 5. `device_sn` (Device Serial Number)

- **Scope**: Per-device. Permanent hardware identifier.
- **Expires**: No.
- **How to obtain programmatically**:
  - **BLE manufacturer data**: Bytes 9-16 of the BLE advertisement manufacturer data contain the serial number as uppercase hex. The `scan` command already discovers Plaud devices — it just needs to parse and display the SN.
  - **Cloud API**: `GET https://api.plaud.ai/device/list` with `Authorization: bearer <api_token>` returns a list of devices with serial numbers.
  - **Physically**: Likely printed on the device or packaging.
- **Existing code**:
  - TypeScript: `opendict_source/plaud-sdk/PlaudAPI.ts` — `parseSerialNumber()` extracts SN from BLE manufacturer data. `getDevices()` fetches from cloud.
  - Go: BLE scan exists in `aplaud/internal/ble/` but does not extract the SN from manufacturer data.
- **Setup wizard approach**: Scan BLE for Plaud devices, parse the manufacturer data to extract the SN. Fallback: prompt user to type it, or fetch from cloud API.

---

## Keys That Can Be Fully Automated

| Key | Can wizard obtain it? | Dependency |
|-----|----------------------|------------|
| `api_token` | **Yes** — email/password login | User provides email + password |
| `device_sn` | **Yes** — BLE scan or cloud API | Requires api_token (for cloud) or BLE adapter |
| `sn_signature_b64` | **Yes** — `POST /device/sn-sign` | Requires api_token + device_sn |
| `e2ee_rsa_key_path` | **Yes** — `POST /e2ee/gen_key` | Requires api_token |
| `rsa_private_key_pem` | **Yes** — self-generated locally | No cloud or phone app dependency |

---

## Unused Keys (Safe to Remove)

These are defined in the config struct but never referenced in the application:

| Key | Why unused |
|-----|-----------|
| `sdk_app_key` | SDK auth module (`auth/sdk.go`) exists but `NewSDKAuth()` is never called |
| `sdk_app_secret` | Same as above. Also has a bug (line 32 assigns `cachePath` to `appSecret`) |
| `device_sn` | Defined in config struct but never read by any code outside `config.go` |
| `sdk_token_cache_path` | Would be used by SDK auth, but that module is not wired up |

Additionally, `config.py` (Python) has `RSA_PUBLIC_KEY_PEM` which has no equivalent in the Go config struct.

---

## Proposed Setup Wizard Flow (`aplaud setup`)

```
1. Check for existing config at ~/.config/aplaud/config.yaml
   - If found, offer to reconfigure or update specific keys

2. Prompt for Plaud account email and password
   - POST /auth/access-token with region auto-detection
   - Save api_token to config

3. Fetch E2EE RSA key
   - POST /e2ee/gen_key
   - Save to ~/.config/aplaud/plaud_rsa_key (backup existing if present)

4. Discover device
   - Option A: BLE scan for Plaud devices, extract SN from manufacturer data
   - Option B: GET /device/list from cloud API
   - Prompt user to confirm if multiple devices found

5. Generate BLE transport RSA key
    - Generate a fresh RSA-2048 key pair locally
    - No extraction from phone app needed — device accepts any key during session
    - (See BLE_KEY_EXCHANGE_FINDINGS.md for analysis)

6. Fetch SN signature
   - POST /device/sn-sign with api_token, device_sn, and (optionally) RSA public key
   - Save sn_signature_b64 to config

7. Write complete config.yaml

8. Verify: attempt a test BLE connection to confirm all keys work
```

---

## Implementation Notes

### Email/Password Login (new code needed in Go)

The Go app currently only has SDK token exchange. A `login()` function calling `/auth/access-token` is needed. Reference implementation in TypeScript at `opendict_source/plaud-sdk/PlaudAPI.ts:38-63`.

### SN Signature Fetch (new code needed in Go)

No Go implementation exists. Need to add a function that calls `POST /device/sn-sign`. Reference in TypeScript at `opendict_source/plaud-sdk/PlaudAPI.ts:80-103`.

### BLE Manufacturer Data Parsing (enhancement needed)

The existing BLE scan in `aplaud/internal/ble/` discovers Plaud devices but does not parse the serial number from manufacturer data. Need to extract bytes 9-16 and format as uppercase hex. Reference in TypeScript at `opendict_source/plaud-sdk/PlaudAPI.ts:127-131`.

### Fresh RSA Key Generation (confirmed viable)

The device accepts any RSA-2048 public key sent during the pre-handshake (cmd 0xFE12). No need to pass the public key to `/device/sn-sign` — the device receives it directly via BLE. A setup wizard should:
1. Generate a 2048-bit RSA key pair locally
2. Save the private key to config
3. Send the public key during BLE pre-handshake (step 3 of the connection flow)

See `BLE_KEY_EXCHANGE_FINDINGS.md` for the full analysis.

### Bug Fixes Needed

- `aplaud/internal/auth/sdk.go:32` — `appSecret: cachePath` should be `appSecret: appSecret`
- `aplaud/internal/auth/sdk.go:96` — Authorization header uses `Bearer` but the SDK token endpoint expects `Basic` for the first step (the code does use `Basic` correctly via the base64 encoding, but the comment says "Bearer")

### API Region Handling

The Plaud API has multiple regional endpoints:
- `api.plaud.ai` (US)
- `api-euc1.plaud.ai` (EU)
- `api-apse1.plaud.ai` (AP Southeast — used for E2EE)
- `platform.plaud.cn` (China — used for SDK token exchange)

The login endpoint supports region auto-detection via `status: -302` redirects. The E2EE endpoint appears to be hardcoded to `api-apse1.plaud.ai`. The SN signature endpoint uses the same region as the login. A setup wizard should store the user's region and use it consistently.
