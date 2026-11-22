//! Normative v3 error codes (`docs/spec/errors.md`).

use serde::{Deserialize, Serialize};
use thiserror::Error;

/// Protocol disconnect codes carried in `BYE` (`PW_*`).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[repr(u16)]
pub enum ByeCode {
    Normal = 0,
    VersionMismatch = 1,
    UnsupportedMessage = 2,
    AuthFailed = 3,
    Banned = 4,
    NotInvited = 5,
    RoomFull = 6,
    ProtocolViolation = 7,
    AuthorityViolation = 8,
    RateLimited = 9,
    EnvelopeTooLarge = 10,
    CborInvalid = 11,
    Timeout = 12,
    HostLost = 13,
    StreamReplaced = 14,
    ModeMismatch = 15,
    Internal = 16,
}

/// Stateless signaling relay error codes (`RL_*`).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[repr(u16)]
pub enum RelayErrorCode {
    Ok = 0,
    BadFrame = 1,
    UnknownMessage = 2,
    FrameTooLarge = 3,
    ChannelFull = 4,
    ChannelQuota = 5,
    AliasTaken = 6,
    NotSubscribed = 7,
    NoSuchAlias = 8,
    BytesExceeded = 9,
    RateLimited = 10,
    IdleTimeout = 11,
    ShuttingDown = 12,
    Unauthorized = 13,
}

/// Local-only error codes (`L_*`, codes 100..599, never sent on wire).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[repr(u16)]
pub enum LocalErrorCode {
    PlayerInit = 100,
    PlayerCodecUnsupported = 101,
    PlayerAppendRejected = 102,
    PlayerQuota = 103,
    PlayerDecode = 104,
    PlayerSeekFailed = 105,
    PlayerMpvExited = 106,
    PlayerMpvIpc = 107,
    PlayerAutoplayBlocked = 108,
    PlayerAppendTimeout = 109,

    StoreIo = 200,
    StoreQuota = 201,
    StoreCorrupt = 202,
    StoreScrubFailed = 203,
    StoreUnavailable = 204,

    MediaNoFfmpeg = 300,
    MediaProbeFailed = 301,
    MediaUnsupportedCodec = 302,
    MediaEncoderFailed = 303,
    MediaBoxTooLarge = 304,
    MediaNoKeyframes = 305,
    MediaUnderrun = 306,
    MediaTimeout = 307,
    MediaMoovMissing = 308,

    NetNoTurn = 400,
    NetIceFailed = 401,
    NetStunUnreachable = 402,
    NetRelayUnreachable = 403,
    NetDataChannelFailed = 404,

    PlatformNoMse = 500,
    PlatformNoOpfs = 501,
    PlatformNoWorker = 502,
    PlatformBackground = 503,
    PlatformXdg = 504,
}

/// Rich in-process disconnect reason distinguishing transport failure from `BYE`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum DisconnectReason {
    Bye(ByeCode),
    IceFailed,
    ConsentExpired,
    ChannelClosed,
    LocalShutdown,
}

#[derive(Debug, Error)]
pub enum CoreError {
    #[error("invalid block index {block} for segment with {block_count} blocks")]
    BlockOutOfRange { block: u32, block_count: u32 },
    #[error("merkle proof verification failed")]
    InvalidMerkleProof,
    #[error("bitmap decoding failed: {0}")]
    InvalidBitmap(&'static str),
    #[error("collaborative-only action rejected in democratic room")]
    ModeMismatch,
}
