// Direct PLAUD API client — authenticates via email/password,
// calls /device/sn-sign to obtain the SN signature needed for BLE pre-handshake.
// Source: https://github.com/naeemarsalan/opendict

import AsyncStorage from '@react-native-async-storage/async-storage';

const API_URLS = {
  US: 'https://api.plaud.ai',
  EU: 'https://api-euc1.plaud.ai',
};

const TOKEN_KEY = 'plaud_auth_token';
const REGION_KEY = 'plaud_api_region';

export interface PlaudAuthState {
  token: string | null;
  region: 'US' | 'EU';
}

let cachedToken: string | null = null;
let cachedRegion: 'US' | 'EU' = 'EU';

function getBaseUrl(): string {
  return API_URLS[cachedRegion];
}

export const PlaudAPI = {
  async loadAuth(): Promise<PlaudAuthState> {
    const [token, region] = await Promise.all([
      AsyncStorage.getItem(TOKEN_KEY),
      AsyncStorage.getItem(REGION_KEY),
    ]);
    cachedToken = token;
    cachedRegion = (region as 'US' | 'EU') || 'EU';
    return {token: cachedToken, region: cachedRegion};
  },

  async login(email: string, password: string): Promise<{success: boolean; error?: string}> {
    for (const region of [cachedRegion, cachedRegion === 'EU' ? 'US' : 'EU'] as const) {
      const url = `${API_URLS[region]}/auth/access-token`;
      const body = `username=${encodeURIComponent(email)}&password=${encodeURIComponent(password)}`;
      const resp = await fetch(url, {
        method: 'POST',
        headers: {'Content-Type': 'application/x-www-form-urlencoded'},
        body,
      });
      const data = await resp.json();
      if (data.status === -302 && data.data?.domains?.api) {
        const redirectDomain = data.data.domains.api;
        const redirectRegion = redirectDomain.includes('euc1') ? 'EU' : 'US';
        cachedRegion = redirectRegion;
        continue;
      }
      if (data.status === 0 && data.access_token) {
        cachedToken = data.access_token;
        cachedRegion = region;
        await AsyncStorage.setItem(TOKEN_KEY, cachedToken!);
        await AsyncStorage.setItem(REGION_KEY, cachedRegion);
        return {success: true};
      }
      return {success: false, error: data.msg || 'Login failed'};
    }
    return {success: false, error: 'Login failed on all regions'};
  },

  async loginWithToken(token: string, region: 'US' | 'EU' = 'EU'): Promise<{success: boolean; error?: string}> {
    cachedRegion = region;
    const url = `${API_URLS[region]}/user/me`;
    const resp = await fetch(url, { headers: { Authorization: `bearer ${token}` } });
    const data = await resp.json();
    if (data.status === 0) {
      cachedToken = token;
      await AsyncStorage.setItem(TOKEN_KEY, token);
      await AsyncStorage.setItem(REGION_KEY, region);
      return {success: true};
    }
    return {success: false, error: 'Invalid token'};
  },

  async getSnSignature(serialNumber: string, rsaPublicKeyPem?: string): Promise<string> {
    if (!cachedToken) throw new Error('Not authenticated — login first');
    const url = `${getBaseUrl()}/device/sn-sign`;
    const body: any = {sns: [serialNumber]};
    if (rsaPublicKeyPem) {
      const pubKeyBase64 = rsaPublicKeyPem
        .replace(/-----BEGIN PUBLIC KEY-----/, '')
        .replace(/-----END PUBLIC KEY-----/, '')
        .replace(/\r?\n/g, '');
      body.public_key_base64 = pubKeyBase64;
    }
    const resp = await fetch(url, {
      method: 'POST',
      headers: {
        Authorization: `bearer ${cachedToken}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(body),
    });
    const data = await resp.json();
    if (data.status === 0 && data.data?.signature?.[serialNumber]) {
      return data.data.signature[serialNumber];
    }
    throw new Error(`SN sign failed: ${data.msg || JSON.stringify(data)}`);
  },

  async getUserInfo(): Promise<any> {
    if (!cachedToken) return null;
    const resp = await fetch(`${getBaseUrl()}/user/me`, { headers: { Authorization: `bearer ${cachedToken}` } });
    const data = await resp.json();
    return data.status === 0 ? data.data_user : null;
  },

  async getDevices(): Promise<any[]> {
    if (!cachedToken) return [];
    const resp = await fetch(`${getBaseUrl()}/device/list`, { headers: { Authorization: `bearer ${cachedToken}` } });
    const data = await resp.json();
    return data.status === 0 ? data.data_devices || [] : [];
  },

  isAuthenticated(): boolean { return !!cachedToken; },

  async logout(): Promise<void> {
    cachedToken = null;
    await AsyncStorage.removeItem(TOKEN_KEY);
  },

  parseSerialNumber(manufacturerData: Uint8Array): string {
    if (manufacturerData.length < 17) return '';
    const snBytes = manufacturerData.slice(9, 17);
    return Array.from(snBytes).map(b => b.toString(16).padStart(2, '0').toUpperCase()).join('');
  },
};
