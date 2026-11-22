//! Normative `Event` / `Command` boundary (`docs/spec/core-api.md`).

use bytes::Bytes;
use serde::{Deserialize, Serialize};

use crate::errors::{ByeCode, DisconnectReason, LocalErrorCode};
use crate::sync::{PlayState, SyncConfig};
use crate::types::{
    Alias, Channel, ChannelId, EncPubKeyBytes, Hash32, PeerId, PublicKeyBytes, RoomId, RoomMode,
    StreamId,
};

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
pub struct TimerId(pub u32);

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
pub struct AppendToken(pub u64);

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum SendClass {
    Control,
    Bulk,
    Data,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Identity {
    pub peer_id: PeerId,
    pub sig_pubkey: PublicKeyBytes,
    pub enc_pubkey: EncPubKeyBytes,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct CoreConfig {
    pub default_mode: RoomMode,
    pub sync: SyncConfig,
    pub sctp_low_watermark: u32,
    pub sctp_high_watermark: u32,
}

impl Default for CoreConfig {
    fn default() -> Self {
        Self {
            default_mode: RoomMode::Democratic,
            sync: SyncConfig::default(),
            sctp_low_watermark: crate::types::SCTP_LOW_WATERMARK,
            sctp_high_watermark: crate::types::SCTP_HIGH_WATERMARK,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum AppendReject {
    QuotaExceeded,
    DecodeError,
    InvalidState,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct SubtitleTrack {
    pub id: u32,
    pub lang: String,
    pub label: String,
    pub byte_len: u32,
    pub hash: Hash32,
    pub bytes: Option<Bytes>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub enum PlayerCommand {
    Load {
        stream_id: StreamId,
        mime: String,
        init_segment: Bytes,
        subtitles: Vec<SubtitleTrack>,
    },
    Play,
    Pause,
    Seek {
        t_ms: u64,
    },
    SetRate {
        rate: f32,
    },
    AppendSegment {
        seg: u32,
        bytes: Bytes,
        token: AppendToken,
    },
    Flush,
    Evict {
        up_to_ms: u64,
    },
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum PlayerEvent {
    Position {
        t_ms: u64,
    },
    BufferedAhead {
        ms: u64,
    },
    State {
        playing: bool,
        buffering: bool,
        ended: bool,
    },
    SeekComplete {
        t_ms: u64,
    },
    AppendDone {
        token: AppendToken,
    },
    AppendRejected {
        token: AppendToken,
        reason: AppendReject,
    },
    Error {
        code: LocalErrorCode,
        message: String,
    },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum VideoEncoder {
    AutoHw,
    Vaapi,
    Nvenc,
    VideoToolbox,
    Qsv,
    LibX264,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum Pipeline {
    Remux,
    TranscodeAhead {
        encoder: VideoEncoder,
        crf: u8,
        maxrate: String,
    },
    BrowserTransmux,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum MediaCommand {
    Probe {
        stream: StreamId,
        source_ref: String,
    },
    Start {
        stream: StreamId,
        source_ref: String,
        pipeline: Pipeline,
        audio_stream: Option<u32>,
        sub_streams: Vec<u32>,
        start_ms: u64,
    },
    SeekSource {
        stream: StreamId,
        target_ms: u64,
    },
    Throttle {
        stream: StreamId,
        max_encode_ahead_ms: u64,
    },
    Stop {
        stream: StreamId,
    },
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub enum UiCommand {
    CreateRoom {
        mode: RoomMode,
        invites: u32,
    },
    JoinRoom {
        invite: String,
    },
    LeaveRoom,
    ProposeSource {
        source_ref: String,
        audio_stream: Option<u32>,
        sub_streams: Vec<u32>,
    },
    CastVote {
        proposal_id: Hash32,
        approve: bool,
    },
    QueueAdd {
        source_ref: String,
    },
    QueueRemove {
        entry_id: Hash32,
    },
    RoomPlay,
    RoomPause,
    RoomSeek {
        t_ms: u64,
    },
    RoomSetRate {
        rate: f32,
    },
    LocalPause,
    LocalSeek {
        t_ms: u64,
    },
    ReturnToLive,
    SendChat {
        body: String,
    },
    Kick {
        peer: PeerId,
        reason: String,
    },
    RequestSnapshot,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub enum UiEvent {
    RoomReady {
        room_id: RoomId,
        mode: RoomMode,
    },
    PlaybackState {
        state: PlayState,
        t_media_ms: u64,
        rate: f32,
        host: Option<PeerId>,
    },
    FreeRoam {
        delta_ms: i64,
    },
    ChatMessage {
        from: PeerId,
        body: String,
        ts_ms: u64,
        lamport: u64,
    },
    Error {
        code: LocalErrorCode,
        text: String,
    },
}

/// Inputs fed into `pw-core` by a shell.
#[derive(Debug, Clone)]
pub enum Event {
    Start {
        config: CoreConfig,
        identity: Identity,
        now_ms: u64,
    },
    Shutdown,
    Tick {
        now_ms: u64,
    },
    Timer {
        id: TimerId,
        now_ms: u64,
    },
    Entropy {
        bytes: Vec<u8>,
    },
    PeerConnected {
        peer: PeerId,
        dtls_fingerprint: [u8; 32],
    },
    PeerChannelOpen {
        peer: PeerId,
        channel: Channel,
    },
    PeerDisconnected {
        peer: PeerId,
        reason: DisconnectReason,
    },
    MessageReceived {
        peer: PeerId,
        channel: Channel,
        bytes: Bytes,
    },
    TransportBackpressure {
        peer: PeerId,
        aggregate_buffered_bytes: u32,
    },
    RelayConnected,
    RelayDisconnected {
        reason: String,
    },
    RelayRoster {
        channel: ChannelId,
        aliases: Vec<Alias>,
    },
    RelayEnvelope {
        channel: ChannelId,
        from_alias: Alias,
        bytes: Bytes,
    },
    BlockLoaded {
        stream: StreamId,
        seg: u32,
        block: u16,
        data: Bytes,
    },
    BlockMissing {
        stream: StreamId,
        seg: u32,
        block: u16,
    },
    SegmentDurable {
        stream: StreamId,
        seg: u32,
    },
    Player(PlayerEvent),
    MediaSegment {
        stream: StreamId,
        seg: u32,
        t_start_ms: u64,
        t_end_ms: u64,
        bytes: Bytes,
    },
    Ui(UiCommand),
}

/// Pure commands emitted by `pw-core` for the shell to execute.
#[derive(Debug, Clone)]
pub enum Command {
    ConnectPeer {
        peer: PeerId,
        alias: Alias,
    },
    Send {
        peer: PeerId,
        channel: Channel,
        bytes: Bytes,
        class: SendClass,
    },
    ClosePeer {
        peer: PeerId,
        code: ByeCode,
        reason: String,
    },
    RelaySubscribe {
        channel: ChannelId,
        alias: Alias,
    },
    RelayUnsubscribe {
        channel: ChannelId,
    },
    RelaySend {
        channel: ChannelId,
        to: Alias,
        bytes: Bytes,
    },
    PersistSegment {
        stream: StreamId,
        seg: u32,
        bytes: Bytes,
    },
    LoadBlock {
        stream: StreamId,
        seg: u32,
        block: u16,
    },
    InvalidateSegment {
        stream: StreamId,
        seg: u32,
    },
    FlushStore,
    Player(PlayerCommand),
    Media(MediaCommand),
    SetTimer {
        id: TimerId,
        after_ms: u64,
    },
    CancelTimer {
        id: TimerId,
    },
    EmitUi {
        event: UiEvent,
    },
}
