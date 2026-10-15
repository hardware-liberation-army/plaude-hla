// Original file transfer implementation for the PLAUD BLE protocol.
// Manages the full download lifecycle: list, prepare, stream, stop.
// Source: https://github.com/naeemarsalan/opendict

import RNFS from 'react-native-fs';
import {PlaudBleManager} from './PlaudBleManager';
import {CMD, EMPTY_PACKAGE_OFFSET} from './constants';
import {
  buildGetFileListPacket,
  buildFileSyncPreparePacket,
  buildSyncFileHeadPacket,
  buildFileSyncStopPacket,
  concatBuffers,
} from './PacketBuilder';
import {
  parseFileListResponse,
  parseFileSyncPrepareResponse,
  parseFileDataPacket,
  isEmptyPackage,
  parseEmptyPackageStatus,
  type FileListEntry,
} from './PacketParser';

type ProgressCallback = (progress: number, bytesTransferred: number) => void;

class PlaudFileSyncImpl {
  private currentSessionId: number | null = null;
  private cancelled = false;

  async getFileList(fileType: number = 0): Promise<FileListEntry[]> {
    const connection = PlaudBleManager.getConnection();
    if (!connection?.isConnected) {
      throw new Error('Not connected to device');
    }
    const packet = buildGetFileListPacket(fileType);
    const response = await connection.sendCommand(packet, CMD.GET_FILE_LIST);
    return parseFileListResponse(response);
  }

  async downloadFile(
    recordingId: string,
    fileIndex: number,
    fileType: number,
    onProgress: ProgressCallback,
  ): Promise<string> {
    const connection = PlaudBleManager.getConnection();
    if (!connection?.isConnected) {
      throw new Error('Not connected to device');
    }
    this.cancelled = false;

    // Step 1: Prepare file transfer
    const preparePacket = buildFileSyncPreparePacket(fileType, fileIndex);
    const prepareResponse = await connection.sendCommand(
      preparePacket,
      CMD.FILE_SYNC_PREPARE,
    );
    const {sessionId, fileSize} = parseFileSyncPrepareResponse(prepareResponse);
    this.currentSessionId = sessionId;

    if (fileSize === 0) {
      throw new Error('File is empty');
    }

    // Step 2: Start file data streaming
    const headPacket = buildSyncFileHeadPacket(sessionId, 0, fileSize);
    const fileBuffer: Uint8Array[] = [];
    let bytesReceived = 0;
    let expectedOffset = 0;

    const downloadComplete = new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(() => {
        reject(new Error('File transfer timed out'));
      }, 300000);

      connection.setFileDataHandler(data => {
        if (this.cancelled) {
          clearTimeout(timeout);
          reject(new Error('Transfer cancelled'));
          return;
        }
        try {
          const packet = parseFileDataPacket(data);

          // Check for end-of-file marker
          if (isEmptyPackage(packet.offset)) {
            const status = parseEmptyPackageStatus(data, connection.devicePortVersion);
            clearTimeout(timeout);
            if (status === 1) {
              resolve();
            } else {
              reject(new Error(`Transfer ended with error status: ${status}`));
            }
            return;
          }

          // Check for offset mismatch (packet loss)
          if (packet.offset !== expectedOffset) {
            console.warn(`Offset mismatch: expected ${expectedOffset}, got ${packet.offset}. Requesting resend.`);
            const resendPacket = buildSyncFileHeadPacket(sessionId, expectedOffset, fileSize);
            connection.writeCommand(resendPacket).catch(err => {
              clearTimeout(timeout);
              reject(err);
            });
            return;
          }

          fileBuffer.push(packet.data);
          bytesReceived += packet.data.length;
          expectedOffset = bytesReceived;
          const progress = Math.min(bytesReceived / fileSize, 1);
          onProgress(progress, bytesReceived);
        } catch (err) {
          clearTimeout(timeout);
          reject(err);
        }
      });
    });

    await connection.writeCommand(headPacket);
    await downloadComplete;
    connection.setFileDataHandler(null);

    // Step 3: Send stop command
    const stopPacket = buildFileSyncStopPacket(sessionId, 0);
    await connection.writeCommand(stopPacket).catch(() => {});
    this.currentSessionId = null;

    // Step 4: Save to local file (raw Opus)
    const fullData = concatBuffers(...fileBuffer);
    const filePath = `${RNFS.DocumentDirectoryPath}/recordings/${recordingId}.opus`;
    await RNFS.mkdir(`${RNFS.DocumentDirectoryPath}/recordings`);
    const base64Data = bytesToBase64Rnfs(fullData);
    await RNFS.writeFile(filePath, base64Data, 'base64');
    return filePath;
  }

  async cancelSync(): Promise<void> {
    this.cancelled = true;
    if (this.currentSessionId !== null) {
      const connection = PlaudBleManager.getConnection();
      if (connection?.isConnected) {
        const stopPacket = buildFileSyncStopPacket(this.currentSessionId, 1);
        await connection.writeCommand(stopPacket).catch(() => {});
      }
      this.currentSessionId = null;
    }
  }
}

function bytesToBase64Rnfs(bytes: Uint8Array): string {
  let binary = '';
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary);
}

export const PlaudFileSync = new PlaudFileSyncImpl();
