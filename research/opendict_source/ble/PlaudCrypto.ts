// Original encryption layer for PLAUD BLE protocol (portVersion >= 20).
// Uses ChaCha20-Poly1305 via @noble/ciphers and RSA via node-forge.
// Source: https://github.com/naeemarsalan/opendict

import forge from 'node-forge';
import {chacha20poly1305} from '@noble/ciphers/chacha';
import {CMD, CHUNK_SIZE} from './constants';
import {
  buildPreHandshakeChunk,
  splitIntoChunks,
  base64ToBytes,
  bytesToBase64,
  concatBuffers,
  writeUint32LE,
} from './PacketBuilder';
import {parsePreHandshakeResponse} from './PacketParser';
import type {PlaudConnection} from './PlaudConnection';

interface SessionKeys {
  key: Uint8Array;   // 32 bytes
  nonce: Uint8Array; // 12 bytes
  ad: Uint8Array;    // 12 bytes
}

export class PlaudCrypto {
  private keys: SessionKeys | null = null;
  private sendCounter = 0;
  private receiveCounter = 0;

  get isReady(): boolean {
    return this.keys !== null;
  }

  async performPreHandshake(
    connection: PlaudConnection,
    snSignatureBase64: string,
    rsaPublicKeyPem: string,
    rsaPrivateKeyPem: string,
    isNotePro: boolean = false,
  ): Promise<void> {
    const preHandshakeCmd = isNotePro ? CMD.PRE_HANDSHAKE_NEW : CMD.PRE_HANDSHAKE;
    console.log(`[PreHS] Using opcode 0x${preHandshakeCmd.toString(16)}`);

    // Step 1: Register for 0xFE11 confirmation, then send SN signature
    const confirmPromise = connection.waitForResponse(CMD.PRE_HANDSHAKE_CNF, 10000);

    console.log('[PreHS] Sending SN signature...');
    const snBytes = base64ToBytes(snSignatureBase64);
    const snChunks = splitIntoChunks(snBytes, CHUNK_SIZE);

    for (let i = 0; i < snChunks.length; i++) {
      const packet = buildPreHandshakeChunk(preHandshakeCmd, snChunks.length, i, snChunks[i]);
      await connection.writeCommand(packet);
    }

    // Step 2: Wait for 0xFE11
    console.log('[PreHS] Waiting for 0xFE11...');
    const confirmData = await confirmPromise;
    console.log(`[PreHS] Got 0xFE11 (${confirmData.length} bytes)`);

    // Step 3: Collect 0xFE12 response chunks, then send RSA public key
    const rsaResponseChunks: Map<number, Uint8Array> = new Map();
    let totalExpected = -1;

    const keyMaterialPromise = new Promise<Uint8Array>((resolve, reject) => {
      const timeout = setTimeout(() => {
        reject(new Error('Pre-handshake key exchange timed out'));
      }, 15000);

      connection.commandQueue.set(CMD.SEND_RSA_PUBKEY, {
        resolve: (data: Uint8Array) => {
          const total = data[2];
          const idx = data[3];
          const payload = data.slice(4);
          console.log(`[PreHS] Got 0xFE12 chunk ${idx}/${total} (${payload.length} bytes)`);
          rsaResponseChunks.set(idx, payload);
          totalExpected = total;

          if (rsaResponseChunks.size >= totalExpected) {
            clearTimeout(timeout);
            connection.commandQueue.delete(CMD.SEND_RSA_PUBKEY);
            const ordered: Uint8Array[] = [];
            for (let j = 0; j < totalExpected; j++) {
              const chunk = rsaResponseChunks.get(j);
              if (!chunk) { reject(new Error(`Missing chunk ${j}`)); return; }
              ordered.push(chunk);
            }
            resolve(concatBuffers(...ordered));
          }
        },
        reject: (err: Error) => { clearTimeout(timeout); reject(err); },
        streaming: true,
      });
    });

    console.log('[PreHS] Sending RSA public key...');
    const pubKeyBytes = new Uint8Array(rsaPublicKeyPem.length);
    for (let i = 0; i < rsaPublicKeyPem.length; i++) {
      pubKeyBytes[i] = rsaPublicKeyPem.charCodeAt(i);
    }
    const pubKeyChunks = splitIntoChunks(pubKeyBytes, CHUNK_SIZE);

    for (let i = 0; i < pubKeyChunks.length; i++) {
      const packet = buildPreHandshakeChunk(CMD.SEND_RSA_PUBKEY, pubKeyChunks.length, i, pubKeyChunks[i]);
      await connection.writeCommand(packet);
    }

    // Step 4: Wait for key material, RSA decrypt
    console.log('[PreHS] Waiting for session keys...');
    const keyMaterial = await keyMaterialPromise;
    console.log(`[PreHS] Got ${keyMaterial.length} bytes of key material`);

    const decrypted = await this.rsaDecrypt(keyMaterial, rsaPrivateKeyPem);
    console.log(`[PreHS] RSA decrypted: ${decrypted.length} bytes`);

    if (decrypted.length < 56) {
      throw new Error(`Decrypted key material too short: ${decrypted.length} bytes`);
    }

    this.keys = {
      key: decrypted.slice(0, 32),
      nonce: decrypted.slice(32, 44),
      ad: decrypted.slice(44, 56),
    };

    // Step 6: Verify "PLAUD.AI"
    if (decrypted.length > 56) {
      const verificationBytes = decrypted.slice(56);
      try {
        const verifyResult = await this.chacha20Decrypt(verificationBytes);
        const verifyStr = Array.from(verifyResult).map(b => String.fromCharCode(b)).join('');
        if (!verifyStr.includes('PLAUD.AI')) {
          console.error('[PreHS] VERIFICATION FAILED - keys may be wrong!');
        } else {
          console.log('[PreHS] Verification PASSED - keys are correct');
        }
      } catch (err: any) {
        console.error(`[PreHS] Verification decrypt FAILED: ${err.message}`);
      }
    }

    // Counter starts at 1 (from decompiled r4.java: H=1, H++ before first use → counter=2)
    this.sendCounter = 1;
    this.receiveCounter = 0;
  }

  async encrypt(plaintext: Uint8Array): Promise<Uint8Array> {
    if (!this.keys) return plaintext;
    this.sendCounter++;
    const counterBytes = new Uint8Array(4);
    writeUint32LE(counterBytes, 0, this.sendCounter);
    const toEncrypt = concatBuffers(counterBytes, plaintext);
    return this.chacha20Encrypt(toEncrypt);
  }

  async decrypt(ciphertext: Uint8Array): Promise<Uint8Array> {
    if (!this.keys) return ciphertext;
    const decrypted = await this.chacha20Decrypt(ciphertext);
    const counter = decrypted[0] | (decrypted[1] << 8) | (decrypted[2] << 16) | (decrypted[3] << 24);
    if (counter <= this.receiveCounter) {
      console.warn(`Replay detected: got counter ${counter}, expected > ${this.receiveCounter}`);
    }
    this.receiveCounter = counter;
    return decrypted.slice(4);
  }

  private async chacha20Encrypt(plaintext: Uint8Array): Promise<Uint8Array> {
    if (!this.keys) throw new Error('Encryption not available');
    const cipher = chacha20poly1305(this.keys.key, this.keys.nonce, this.keys.ad);
    return cipher.encrypt(plaintext);
  }

  private async chacha20Decrypt(ciphertext: Uint8Array): Promise<Uint8Array> {
    if (!this.keys) throw new Error('Decryption not available');
    const cipher = chacha20poly1305(this.keys.key, this.keys.nonce, this.keys.ad);
    return cipher.decrypt(ciphertext);
  }

  private async rsaDecrypt(data: Uint8Array, privateKeyPem: string): Promise<Uint8Array> {
    const privateKey = forge.pki.privateKeyFromPem(privateKeyPem);
    const encrypted = forge.util.binary.raw.encode(data);
    const decrypted = privateKey.decrypt(encrypted, 'RSAES-PKCS1-V1_5');
    const bytes = new Uint8Array(decrypted.length);
    for (let i = 0; i < decrypted.length; i++) {
      bytes[i] = decrypted.charCodeAt(i);
    }
    return bytes;
  }
}
