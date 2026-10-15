// Bridge to native OpusDecoder module.
// Decodes raw Opus frames from PLAUD device to WAV files.
// Source: https://github.com/naeemarsalan/opendict

import {NativeModules} from 'react-native';

const {OpusDecoderModule} = NativeModules;

export const OpusDecoder = {
  /**
   * Decode an Opus file to WAV format.
   * @param inputPath Path to the raw Opus frame file
   * @param outputPath Path for the output WAV file
   * @param channels 1 for mono, 2 for stereo (from device handshake)
   * @param sampleRate Sample rate in Hz (default 16000 for PLAUD)
   * @param isOgg Whether the input is in OGG container format
   */
  async decode(
    inputPath: string,
    outputPath: string,
    channels: number = 1,
    sampleRate: number = 16000,
    isOgg: boolean = false,
  ): Promise<string> {
    if (!OpusDecoderModule) {
      throw new Error('OpusDecoderModule not available. Ensure the native module is linked.');
    }
    return OpusDecoderModule.decode(inputPath, outputPath, channels, sampleRate, isOgg);
  },
};
