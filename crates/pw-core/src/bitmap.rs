//! Compact `RAW` / `RLE` segment availability bitmaps (`docs/spec/protocol.md §6.7`).
//!
//! For a 2-hour movie (~1,800 segments), raw encoding is 226 bytes worst case
//! and RLE encoding is 8 bytes for a complete seeder.

use serde::{Deserialize, Serialize};

use crate::errors::CoreError;

/// Maximum wire bytes allowed for a single `AVAILABILITY` bitmap (4 KiB).
pub const MAX_BITMAP_BYTES: usize = 4 * 1024;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum BitmapEncoding {
    Raw,
    Rle,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct SegmentBitmap {
    pub encoding: BitmapEncoding,
    pub total_bits: u32,
    pub data: Vec<u8>,
}

impl SegmentBitmap {
    /// Encode a slice of booleans using whichever of `Raw` or `Rle` is smaller.
    pub fn encode(bits: &[bool]) -> Self {
        let total_bits = bits.len() as u32;
        let raw = encode_raw(bits);
        let rle = encode_rle(bits);
        if rle.len() < raw.len() {
            Self {
                encoding: BitmapEncoding::Rle,
                total_bits,
                data: rle,
            }
        } else {
            Self {
                encoding: BitmapEncoding::Raw,
                total_bits,
                data: raw,
            }
        }
    }

    /// Decode and validate bounds before allocating.
    pub fn decode(&self, max_segments: u32) -> Result<Vec<bool>, CoreError> {
        if self.data.len() > MAX_BITMAP_BYTES {
            return Err(CoreError::InvalidBitmap("bitmap exceeds 4 KiB wire cap"));
        }
        if self.total_bits > max_segments {
            return Err(CoreError::InvalidBitmap("total_bits exceeds limit"));
        }
        let n = self.total_bits as usize;
        match self.encoding {
            BitmapEncoding::Raw => {
                let expected = n.div_ceil(8);
                if self.data.len() != expected {
                    return Err(CoreError::InvalidBitmap("raw bitmap length mismatch"));
                }
                let mut out = vec![false; n];
                for (i, slot) in out.iter_mut().enumerate() {
                    *slot = (self.data[i / 8] >> (i % 8)) & 1 == 1;
                }
                Ok(out)
            }
            BitmapEncoding::Rle => {
                let (chunks, rem) = self.data.as_chunks::<8>();
                if !rem.is_empty() {
                    return Err(CoreError::InvalidBitmap("rle payload not 8-byte aligned"));
                }
                let mut out = vec![false; n];
                let mut prev_end = 0usize;
                for chunk in chunks {
                    let start =
                        u32::from_be_bytes([chunk[0], chunk[1], chunk[2], chunk[3]]) as usize;
                    let len = u32::from_be_bytes([chunk[4], chunk[5], chunk[6], chunk[7]]) as usize;
                    if len == 0 || start < prev_end {
                        return Err(CoreError::InvalidBitmap("unsorted or empty rle run"));
                    }
                    let end = start
                        .checked_add(len)
                        .ok_or(CoreError::InvalidBitmap("rle run overflow"))?;
                    if end > n {
                        return Err(CoreError::InvalidBitmap("rle run out of bounds"));
                    }
                    for slot in &mut out[start..end] {
                        *slot = true;
                    }
                    prev_end = end;
                }
                Ok(out)
            }
        }
    }
}

fn encode_raw(bits: &[bool]) -> Vec<u8> {
    let mut out = vec![0u8; bits.len().div_ceil(8)];
    for (i, &bit) in bits.iter().enumerate() {
        if bit {
            out[i / 8] |= 1u8 << (i % 8);
        }
    }
    out
}

fn encode_rle(bits: &[bool]) -> Vec<u8> {
    let mut out = Vec::new();
    let mut i = 0;
    while i < bits.len() {
        if !bits[i] {
            i += 1;
            continue;
        }
        let start = i;
        while i < bits.len() && bits[i] {
            i += 1;
        }
        let len = i - start;
        out.extend_from_slice(&(start as u32).to_be_bytes());
        out.extend_from_slice(&(len as u32).to_be_bytes());
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn complete_two_hour_movie_compresses_to_8_bytes() {
        let bits = vec![true; 1_801];
        let encoded = SegmentBitmap::encode(&bits);
        assert_eq!(encoded.encoding, BitmapEncoding::Rle);
        assert_eq!(encoded.data.len(), 8);
        assert_eq!(encoded.decode(2_000).unwrap(), bits);
    }

    #[test]
    fn checkerboard_falls_back_to_compact_raw_226_bytes() {
        let bits: Vec<bool> = (0..1_801).map(|i| i % 2 == 0).collect();
        let encoded = SegmentBitmap::encode(&bits);
        assert_eq!(encoded.encoding, BitmapEncoding::Raw);
        assert_eq!(encoded.data.len(), 226);
        assert_eq!(encoded.decode(2_000).unwrap(), bits);
    }
}
