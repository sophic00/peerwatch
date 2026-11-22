//! Sliding-window min-delay reference clock + retuned critically-damped PI
//! sync controller (`docs/design/sync.md`).

use std::collections::VecDeque;

use serde::{Deserialize, Serialize};

/// Tunable sync parameters (defaults match `docs/design/sync.md §3`).
#[derive(Debug, Clone, Copy, PartialEq, Serialize, Deserialize)]
pub struct SyncConfig {
    pub kp: f64,
    pub ki: f64,
    pub rate_clamp: f64,
    pub hard_seek_threshold_s: f64,
    pub antiwindup_band_s: f64,
    pub ewma_alpha: f64,
    pub offset_window_len: usize,
}

impl Default for SyncConfig {
    fn default() -> Self {
        Self {
            kp: 0.30,
            ki: 0.0225,
            rate_clamp: 0.05,
            hard_seek_threshold_s: 2.0,
            antiwindup_band_s: 1.5,
            ewma_alpha: 0.25,
            offset_window_len: 8,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum SyncMode {
    Following,
    FreeRoam,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum PlayState {
    Playing,
    Paused,
}

/// Output of one PI controller tick.
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum SyncAction {
    /// Adjust player playback rate smoothly (clamped within ±5% of host rate).
    SetRate(f64),
    /// Error exceeded `hard_seek_threshold_s`: seek cleanly to target media time (ms).
    HardSeek { target_media_ms: u64 },
    /// Pause local playback (optionally snapping if > 300 ms from host pause time).
    Pause { snap_media_ms: Option<u64> },
    /// Follower is in `FreeRoam`; do not move local playhead.
    FreeRoam { delta_ms: i64 },
}

#[derive(Debug, Clone)]
pub struct SyncController {
    cfg: SyncConfig,
    mode: SyncMode,
    host_state: PlayState,
    host_rate: f64,
    host_pause_media_s: f64,
    /// Sliding window of `offset_sample = (t_media + R*minRTT/2) - R*t_local`.
    offset_samples: VecDeque<f64>,
    filtered_offset_s: Option<f64>,
    ewma_err_s: f64,
    integral_s: f64,
}

impl SyncController {
    pub fn new(cfg: SyncConfig) -> Self {
        Self {
            cfg,
            mode: SyncMode::Following,
            host_state: PlayState::Paused,
            host_rate: 1.0,
            host_pause_media_s: 0.0,
            offset_samples: VecDeque::with_capacity(cfg.offset_window_len),
            filtered_offset_s: None,
            ewma_err_s: 0.0,
            integral_s: 0.0,
        }
    }

    pub fn mode(&self) -> SyncMode {
        self.mode
    }

    pub fn enter_free_roam(&mut self) {
        self.mode = SyncMode::FreeRoam;
        self.reset_pi();
    }

    pub fn return_to_live(&mut self) {
        self.mode = SyncMode::Following;
        self.reset_pi();
    }

    pub fn reset_pi(&mut self) {
        self.ewma_err_s = 0.0;
        self.integral_s = 0.0;
    }

    /// Ingest a Host `SYNC` message received at local monotonic time `now_ms`.
    pub fn on_host_sync(
        &mut self,
        now_ms: u64,
        t_media_ms: u64,
        state: PlayState,
        rate: f64,
        min_rtt_ms: u64,
    ) {
        let rate = rate.clamp(0.25, 4.0);
        let state_changed = state != self.host_state;
        let rate_changed = (rate - self.host_rate).abs() > 1e-4;

        self.host_state = state;
        self.host_rate = rate;

        if state == PlayState::Paused {
            self.host_pause_media_s = (t_media_ms as f64) / 1000.0;
            self.offset_samples.clear();
            self.filtered_offset_s = None;
            self.reset_pi();
            return;
        }

        let now_s = (now_ms as f64) / 1000.0;
        let media_s = (t_media_ms as f64) / 1000.0;
        let one_way_s = (min_rtt_ms as f64) / 2000.0;
        let sample = (media_s + rate * one_way_s) - rate * now_s;

        let jumped = self
            .filtered_offset_s
            .is_some_and(|cur| (sample - cur).abs() > 0.5);

        if state_changed || rate_changed || jumped {
            self.offset_samples.clear();
            self.reset_pi();
        }

        if self.offset_samples.len() == self.cfg.offset_window_len {
            self.offset_samples.pop_front();
        }
        self.offset_samples.push_back(sample);

        // Median of the sliding window rejects isolated Wi-Fi / SCTP delay spikes.
        let mut sorted: Vec<f64> = self.offset_samples.iter().copied().collect();
        sorted.sort_by(|a, b| a.total_cmp(b));
        self.filtered_offset_s = Some(sorted[sorted.len() / 2]);
    }

    /// Current estimated Host media time in seconds at `now_ms`.
    pub fn reference_media_s(&self, now_ms: u64) -> Option<f64> {
        match self.host_state {
            PlayState::Paused => Some(self.host_pause_media_s),
            PlayState::Playing => {
                let offset = self.filtered_offset_s?;
                Some((offset + self.host_rate * (now_ms as f64 / 1000.0)).max(0.0))
            }
        }
    }

    /// Run one controller step given local player position `local_media_ms` and `dt_s`.
    pub fn step(&mut self, now_ms: u64, local_media_ms: u64, dt_s: f64) -> Option<SyncAction> {
        let ref_s = self.reference_media_s(now_ms)?;
        let local_s = (local_media_ms as f64) / 1000.0;
        let err_s = local_s - ref_s;

        if self.mode == SyncMode::FreeRoam {
            return Some(SyncAction::FreeRoam {
                delta_ms: (err_s * 1000.0).round() as i64,
            });
        }

        if self.host_state == PlayState::Paused {
            let snap = if err_s.abs() > 0.3 {
                Some((ref_s * 1000.0).round() as u64)
            } else {
                None
            };
            return Some(SyncAction::Pause {
                snap_media_ms: snap,
            });
        }

        if err_s.abs() > self.cfg.hard_seek_threshold_s {
            self.reset_pi();
            return Some(SyncAction::HardSeek {
                target_media_ms: (ref_s * 1000.0).round() as u64,
            });
        }

        // EWMA filter on position error.
        let alpha = self.cfg.ewma_alpha;
        self.ewma_err_s = alpha * err_s + (1.0 - alpha) * self.ewma_err_s;

        let lo = self.host_rate * (1.0 - self.cfg.rate_clamp);
        let hi = self.host_rate * (1.0 + self.cfg.rate_clamp);
        let raw =
            self.host_rate * (1.0 - self.cfg.kp * self.ewma_err_s - self.cfg.ki * self.integral_s);
        let clamped = raw.clamp(lo, hi);

        // Anti-windup: only integrate inside band when actuator is not saturated.
        let saturated = (raw - clamped).abs() > 1e-9;
        if err_s.abs() < self.cfg.antiwindup_band_s && !saturated && dt_s > 0.0 {
            self.integral_s += self.ewma_err_s * dt_s;
        }

        Some(SyncAction::SetRate(clamped))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn pi_controller_cancels_systematic_clock_skew_and_stays_within_clamp() {
        let mut ctrl = SyncController::new(SyncConfig::default());
        let dt = 0.05; // 20 Hz player feedback
        let mut local_s = 10.0;
        let skew = 1.001; // +0.1% fast local audio crystal

        // Simulate 60 seconds with host starting at 10.8 s (0.8 s ahead of local).
        for step_idx in 0..1200 {
            let now_ms = step_idx * 50;
            if step_idx % 20 == 0 {
                let host_media_ms = 10_800 + now_ms;
                ctrl.on_host_sync(now_ms, host_media_ms, PlayState::Playing, 1.0, 20);
            }
            let action = ctrl
                .step(now_ms, (local_s * 1000.0) as u64, dt)
                .expect("action");
            match action {
                SyncAction::SetRate(rate) => {
                    assert!((0.95..=1.05).contains(&rate), "rate {rate} outside ±5%");
                    local_s += rate * skew * dt;
                }
                other => panic!("unexpected action during small drift: {other:?}"),
            }
        }

        // After 60 s, error must have converged tightly (< 30 ms).
        let final_ref = ctrl.reference_media_s(60_000).unwrap();
        let final_err_ms = ((local_s - final_ref) * 1000.0).abs();
        assert!(
            final_err_ms < 30.0,
            "expected < 30 ms residual drift, got {final_err_ms:.2} ms"
        );
    }

    #[test]
    fn error_above_two_seconds_triggers_hard_seek() {
        let mut ctrl = SyncController::new(SyncConfig::default());
        ctrl.on_host_sync(1_000, 30_000, PlayState::Playing, 1.0, 0);
        let action = ctrl.step(1_000, 27_000, 0.05);
        assert!(matches!(action, Some(SyncAction::HardSeek { .. })));
    }
}
