#pragma once

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <cstdint>

namespace AWeatherApp::Metrics {
inline constexpr std::size_t sample_capacity = 4096;
struct Summary {
    std::size_t count = 0;
    std::uint64_t total_count = 0, rejected = 0;
    double coverage_ms = 0, mean = 0, p50 = 0, p95 = 0, p99 = 0, max = 0, max_since_reset = 0;
};
// Constant storage and O(1) recording. Sorting happens only on status requests.
// Quantiles use nearest rank over the retained window; lifetime count/max remain
// available so a wrapped ring cannot silently conceal its measurement coverage.
class SampleRing {
  public:
    bool add(double value, double timestamp_ms) noexcept {
        if (!std::isfinite(value) || value < 0 || !std::isfinite(timestamp_ms) ||
            timestamp_ms < 0 ||
            (count_ && timestamp_ms < stamps_[(next_ + sample_capacity - 1) % sample_capacity])) {
            ++rejected_;
            return false;
        }
        values_[next_] = value;
        stamps_[next_] = timestamp_ms;
        next_ = (next_ + 1) % sample_capacity;
        count_ = std::min(count_ + 1, sample_capacity);
        ++total_;
        max_ = std::max(max_, value);
        return true;
    }
    void reset() noexcept {
        next_ = count_ = 0;
        total_ = rejected_ = 0;
        max_ = 0;
    }
    Summary summary() const {
        Summary s;
        s.count = count_;
        s.total_count = total_;
        s.rejected = rejected_;
        s.max_since_reset = max_;
        if (!count_)
            return s;
        auto sorted = values_;
        std::sort(sorted.begin(), sorted.begin() + static_cast<std::ptrdiff_t>(count_));
        for (std::size_t i = 0; i < count_; ++i)
            s.mean += (sorted[i] - s.mean) / static_cast<double>(i + 1);
        const auto quantile = [&](double fraction) {
            return sorted[static_cast<std::size_t>(
                              std::ceil(fraction * static_cast<double>(count_))) -
                          1];
        };
        s.p50 = quantile(.50);
        s.p95 = quantile(.95);
        s.p99 = quantile(.99);
        s.max = sorted[count_ - 1];
        const auto oldest = count_ == sample_capacity ? next_ : 0;
        s.coverage_ms = stamps_[(next_ + sample_capacity - 1) % sample_capacity] - stamps_[oldest];
        return s;
    }

  private:
    std::array<double, sample_capacity> values_{}, stamps_{};
    std::size_t next_ = 0, count_ = 0;
    std::uint64_t total_ = 0, rejected_ = 0;
    double max_ = 0;
};

class Intervals {
  public:
    static constexpr double gap_limit_ms = 1000;
    void observe(double timestamp_ms) noexcept {
        if (!std::isfinite(timestamp_ms) || timestamp_ms < 0 ||
            (hasLast_ && timestamp_ms <= last_)) {
            ++invalid_;
            return;
        }
        ++events_;
        if (hasLast_) {
            const double interval = timestamp_ms - last_;
            if (interval > gap_limit_ms) {
                ++gaps_;
                longestGap_ = std::max(longestGap_, interval);
            } else
                samples_.add(interval, timestamp_ms);
        }
        last_ = timestamp_ms;
        hasLast_ = true;
    }
    void breakSequence() noexcept {
        if (hasLast_)
            ++breaks_;
        hasLast_ = false;
    }
    void reset() noexcept {
        samples_.reset();
        last_ = longestGap_ = 0;
        hasLast_ = false;
        events_ = gaps_ = breaks_ = invalid_ = 0;
    }
    Summary summary() const {
        auto s = samples_.summary();
        s.rejected += invalid_;
        return s;
    }
    std::uint64_t events() const noexcept {
        return events_;
    }
    std::uint64_t gaps() const noexcept {
        return gaps_;
    }
    std::uint64_t breaks() const noexcept {
        return breaks_;
    }
    double longestGap() const noexcept {
        return longestGap_;
    }

  private:
    SampleRing samples_;
    double last_ = 0, longestGap_ = 0;
    bool hasLast_ = false;
    std::uint64_t events_ = 0, gaps_ = 0, breaks_ = 0, invalid_ = 0;
};
} // namespace AWeatherApp::Metrics
