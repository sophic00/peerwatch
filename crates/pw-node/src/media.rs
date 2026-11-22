//! Native `ffmpeg` / `ffprobe` media pipeline (`docs/design/media-pipeline.md`).
//!
//! Supports lossless remux, hardware-accelerated seekable live transcode-ahead
//! (VA-API / NVENC / VideoToolbox / QSV with `libx264` fallback), audio track
//! selection, and WebVTT subtitle extraction.

use pw_core::api::VideoEncoder;

/// Encoder selection preference order for the current OS.
pub fn preferred_encoders() -> Vec<VideoEncoder> {
    #[cfg(target_os = "macos")]
    {
        vec![VideoEncoder::VideoToolbox, VideoEncoder::LibX264]
    }
    #[cfg(target_os = "windows")]
    {
        vec![
            VideoEncoder::Nvenc,
            VideoEncoder::Qsv,
            VideoEncoder::LibX264,
        ]
    }
    #[cfg(not(any(target_os = "macos", target_os = "windows")))]
    {
        vec![
            VideoEncoder::Vaapi,
            VideoEncoder::Nvenc,
            VideoEncoder::Qsv,
            VideoEncoder::LibX264,
        ]
    }
}
