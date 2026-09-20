import pako from "pako";

const COMPRESS_THRESHOLD = 256;

/** Compression result. */
export interface CompressResult {
  data: Uint8Array;
  compressed: boolean;
}

/**
 * Gzip-compress data when it exceeds the threshold and the compressed
 * result is actually smaller. Returns the original data when compression
 * is skipped or ineffective.
 */
export function compress(data: Uint8Array): CompressResult {
  if (data.length < COMPRESS_THRESHOLD) {
    return { data, compressed: false };
  }

  const compressed = pako.gzip(data);

  if (compressed.length >= data.length) {
    return { data, compressed: false };
  }

  return { data: compressed, compressed: true };
}

/**
 * Gzip-decompress data. When the input is not compressed the caller
 * should skip this function entirely; passing non-gzip data will throw.
 */
export function decompress(data: Uint8Array): Uint8Array {
  return pako.ungzip(data);
}
