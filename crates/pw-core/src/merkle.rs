//! Domain-separated BLAKE3 block hashing and per-segment Merkle trees
//! (`docs/spec/protocol.md §3.1, §6.6`).

use crate::types::{BLOCK_SIZE, Hash32};

/// Compute `BLAKE3("pw-block-v3" || block_bytes)`.
pub fn hash_block(bytes: &[u8]) -> Hash32 {
    let mut hasher = blake3::Hasher::new();
    hasher.update(b"pw-block-v3");
    hasher.update(bytes);
    *hasher.finalize().as_bytes()
}

/// Compute `BLAKE3("pw-merkle-v3" || left || right)`.
fn hash_pair(left: &Hash32, right: &Hash32) -> Hash32 {
    let mut hasher = blake3::Hasher::new();
    hasher.update(b"pw-merkle-v3");
    hasher.update(left);
    hasher.update(right);
    *hasher.finalize().as_bytes()
}

/// Slice a segment into ≤ 32 KiB blocks and compute every leaf hash.
pub fn segment_leaf_hashes(segment_bytes: &[u8]) -> Vec<Hash32> {
    if segment_bytes.is_empty() {
        return Vec::new();
    }
    segment_bytes.chunks(BLOCK_SIZE).map(hash_block).collect()
}

/// Compute the per-segment Merkle root over `leaves`.
///
/// Odd leaves promote unchanged to the next level. A single-block segment's
/// root is its sole leaf hash.
pub fn compute_segment_root(leaves: &[Hash32]) -> Option<Hash32> {
    if leaves.is_empty() {
        return None;
    }
    let mut level = leaves.to_vec();
    while level.len() > 1 {
        let mut next = Vec::with_capacity(level.len().div_ceil(2));
        for pair in level.chunks(2) {
            if pair.len() == 2 {
                next.push(hash_pair(&pair[0], &pair[1]));
            } else {
                next.push(pair[0]);
            }
        }
        level = next;
    }
    Some(level[0])
}

/// Build the bottom-up sibling Merkle proof for block `block_index`.
pub fn build_proof(leaves: &[Hash32], block_index: usize) -> Option<Vec<Hash32>> {
    if block_index >= leaves.len() {
        return None;
    }
    let mut proof = Vec::new();
    let mut level = leaves.to_vec();
    let mut idx = block_index;

    while level.len() > 1 {
        if idx.is_multiple_of(2) {
            if idx + 1 < level.len() {
                proof.push(level[idx + 1]);
            }
        } else {
            proof.push(level[idx - 1]);
        }

        let mut next = Vec::with_capacity(level.len().div_ceil(2));
        for pair in level.chunks(2) {
            if pair.len() == 2 {
                next.push(hash_pair(&pair[0], &pair[1]));
            } else {
                next.push(pair[0]);
            }
        }
        level = next;
        idx /= 2;
    }

    Some(proof)
}

/// Verify a single block's Merkle proof against a trusted `segment_root`.
pub fn verify_block_proof(
    block_bytes: &[u8],
    block_index: usize,
    block_count: usize,
    proof: &[Hash32],
    expected_root: &Hash32,
) -> bool {
    if block_count == 0 || block_index >= block_count {
        return false;
    }
    let mut current = hash_block(block_bytes);
    let mut idx = block_index;
    let mut width = block_count;
    let mut proof_iter = proof.iter();

    while width > 1 {
        if idx.is_multiple_of(2) {
            if idx + 1 < width {
                let Some(sibling) = proof_iter.next() else {
                    return false;
                };
                current = hash_pair(&current, sibling);
            }
        } else {
            let Some(sibling) = proof_iter.next() else {
                return false;
            };
            current = hash_pair(sibling, &current);
        }
        idx /= 2;
        width = width.div_ceil(2);
    }

    // Reject trailing unused proof hashes.
    if proof_iter.next().is_some() {
        return false;
    }

    current == *expected_root
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn roundtrip_single_even_and_odd_block_counts() {
        for block_count in [1usize, 2, 3, 5, 16, 17, 128] {
            let blocks: Vec<Vec<u8>> = (0..block_count)
                .map(|i| vec![(i as u8).wrapping_mul(37).wrapping_add(1); 1024])
                .collect();
            let leaves: Vec<Hash32> = blocks.iter().map(|b| hash_block(b)).collect();
            let root = compute_segment_root(&leaves).expect("non-empty root");

            for (idx, block) in blocks.iter().enumerate() {
                let proof = build_proof(&leaves, idx).expect("proof");
                assert!(
                    verify_block_proof(block, idx, block_count, &proof, &root),
                    "failed for block_count={block_count}, idx={idx}"
                );

                // Tampered payload must fail.
                let mut bad = block.clone();
                bad[0] ^= 0xFF;
                assert!(!verify_block_proof(&bad, idx, block_count, &proof, &root));
            }
        }
    }
}
