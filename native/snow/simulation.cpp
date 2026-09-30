#include "simulation.hpp"
#include <algorithm>
#include <cmath>
namespace a_weather_app::snow {
namespace {
bool exposed(const Support &s, float x) {
  for (std::size_t i = 0; i < s.span_count; ++i)
    if (x >= s.spans[i].left && x < s.spans[i].right)
      return true;
  return false;
}
void remap_cells(std::array<double, cell_cap> &cells, std::size_t old_count,
                 std::size_t count) {
  std::array<double, cell_cap> next{};
  for (std::size_t i = 0; i < old_count; ++i) {
    const double begin = static_cast<double>(i) / static_cast<double>(old_count);
    const double end = static_cast<double>(i + 1) / static_cast<double>(old_count);
    const auto first = std::min(count - 1, static_cast<std::size_t>(begin * static_cast<double>(count)));
    const auto last = std::min(count - 1, static_cast<std::size_t>(end * static_cast<double>(count)));
    double remaining = cells[i];
    for (std::size_t j = first; j <= last; ++j) {
      const double overlap = std::max(0., std::min(end, static_cast<double>(j + 1) / static_cast<double>(count)) -
                                            std::max(begin, static_cast<double>(j) / static_cast<double>(count)));
      const double amount = j == last ? remaining : std::min(remaining, cells[i] * overlap * static_cast<double>(old_count));
      next[j] += amount;
      remaining -= amount;
    }
  }
  cells = next;
}
bool prepare(std::span<const Support> ss, std::span<Support> out) {
  if (ss.size() > physics::support_cap)
    return false;
  for (std::size_t i = 0; i < ss.size(); ++i) {
    const auto &s = ss[i];
    auto r = s.rect;
    if (!s.id || !std::isfinite(r.x) || !std::isfinite(r.y) ||
        !std::isfinite(r.w) || !std::isfinite(r.h) || r.w <= 0 || r.h <= 0 ||
        r.w > 16384 || r.h > 16384 || std::abs(r.x) > 32768 ||
        std::abs(r.y) > 32768 || s.span_count > physics::span_cap)
      return false;
    for (std::size_t j = 0; j < i; ++j)
      if (ss[j].id == s.id)
        return false;
    float last = r.x;
    for (std::size_t k = 0; k < s.span_count; ++k) {
      auto p = s.spans[k];
      if (!std::isfinite(p.left) || !std::isfinite(p.right) || p.left < last ||
          p.right <= p.left || p.right > r.x + r.w)
        return false;
      last = p.right;
    }
    out[i] = s;
  }
  for (std::size_t i = 0; i < ss.size(); ++i)
    for (std::size_t j = 0; j < ss.size(); ++j) {
      auto &s = out[i];
      const auto &o = ss[j];
      if (i == j || !(o.stack > s.stack || (o.stack == s.stack && j > i)) ||
          o.rect.y > s.rect.y || o.rect.y + o.rect.h <= s.rect.y)
        continue;
      std::array<physics::Span, physics::span_cap> next{};
      std::size_t n = 0;
      auto add = [&](float l, float r) {
        if (r <= l)
          return true;
        if (n == next.size())
          return false;
        next[n++] = {l, r};
        return true;
      };
      for (std::size_t k = 0; k < s.span_count; ++k) {
        auto p = s.spans[k];
        if (o.rect.x >= p.right || o.rect.x + o.rect.w <= p.left) {
          if (!add(p.left, p.right))
            return false;
        } else if (!add(p.left, std::min(p.right, o.rect.x)) ||
                   !add(std::max(p.left, o.rect.x + o.rect.w), p.right))
          return false;
      }
      s.spans = next;
      s.span_count = n;
    }
  return true;
}
Hit sweep(Vec a, Vec b, std::span<const Support> ss) {
  Hit h{};
  float best = 2;
  if (b.y <= a.y)
    return h;
  for (const auto &s : ss) {
    float t = (s.rect.y - a.y) / (b.y - a.y);
    float x = a.x + (b.x - a.x) * t;
    if (t >= 0 && t <= 1 && t < best && x >= s.rect.x + 8 &&
        x <= s.rect.x + s.rect.w - 8 && exposed(s, x)) {
      h = {true, s.id, {x, s.rect.y}, t};
      best = t;
    }
  }
  return h;
}
} // namespace
bool Parameters::valid() const {
  return std::isfinite(strength) && strength >= 0 && strength <= 1 &&
         std::isfinite(wind) && std::abs(wind) <= 500 && std::isfinite(speed) &&
         speed >= 0 && speed <= 3 && std::isfinite(accumulation) &&
         accumulation >= 0 && accumulation <= 5 && std::isfinite(overload) &&
         overload >= 1 && overload <= 100 && std::isfinite(temperature_c) &&
         temperature_c >= -100 && temperature_c <= 65;
}
float Simulation::random(float a, float b) {
  rng_ ^= rng_ >> 12;
  rng_ ^= rng_ << 25;
  rng_ ^= rng_ >> 27;
  return a + (b - a) *
                 static_cast<float>((rng_ * 2685821657736338717ULL) >> 40) /
                 16777216.f;
}
void Simulation::reset(std::uint64_t seed) {
  rng_ = seed ? seed : 7719;
  time_ = 0;
  spawn_credit_ = 0;
  totals_ = {};
  flakes_ = {};
  edges_ = {};
  topology_ = {};
  floor_ = {};
  floor_count_ = 0;
  floor_width_ = 0;
}

double Simulation::floorSnow() const {
  double total = 0;
  for (std::size_t i = 0; i < floor_count_; ++i) total += floor_[i];
  return total;
}

void Simulation::resizeFloor(float width) {
  if (width == floor_width_) return;
  const auto count = static_cast<std::size_t>(std::clamp(
      std::ceil(width / 4), 1.f, static_cast<float>(floor_cell_cap)));
  std::array<double, floor_cell_cap> next{};
  const double cell = static_cast<double>(width) / static_cast<double>(count);
  if (floor_count_) {
    const double previousCell = static_cast<double>(floor_width_) /
                                static_cast<double>(floor_count_);
    for (std::size_t i = 0; i < floor_count_; ++i) {
      const double left = static_cast<double>(i) * previousCell;
      const double right = std::min(static_cast<double>(width), left + previousCell);
      if (right <= left) {
        totals_.escaped += floor_[i];
        continue;
      }
      const double retained = floor_[i] * (right - left) / previousCell;
      totals_.escaped += floor_[i] - retained;
      auto first = static_cast<std::size_t>(std::clamp(std::floor(left / cell),
                                        0., static_cast<double>(count - 1)));
      for (std::size_t j = first; j < count && static_cast<double>(j) * cell < right; ++j) {
        const double overlap = std::max(0., std::min(right, static_cast<double>(j + 1) * cell) -
                                           std::max(left, static_cast<double>(j) * cell));
        next[j] += floor_[i] * overlap / previousCell;
      }
    }
  }
  floor_ = next;
  floor_count_ = count;
  floor_width_ = width;
}

void Simulation::receiveFloor(float x, double volume) {
  const auto i = static_cast<std::size_t>(std::clamp(x / floor_width_, 0.f, .999999f) *
                                        static_cast<float>(floor_count_));
  // At most 24 logical pixels of packed snow. Overflow is explicit drainage,
  // never an unbounded mound or a silently disappearing volume.
  const double capacity = 24. * static_cast<double>(floor_width_) /
                          static_cast<double>(floor_count_);
  const double accepted = std::min(volume, std::max(0., capacity - floor_[i]));
  floor_[i] += accepted;
  totals_.overflow += volume - accepted;
}

bool Simulation::depositFloor(float x, float volume) {
  if (!floor_count_ || !std::isfinite(x) || x < 0 || x >= floor_width_ ||
      !std::isfinite(volume) || volume < 0 || volume > 10000) return false;
  receiveFloor(x, volume);
  totals_.deposited += volume;
  return true;
}
Simulation::Edge *Simulation::edge(std::uint64_t id) {
  for (auto &e : edges_)
    if (e.active && e.support.id == id)
      return &e;
  return nullptr;
}
bool Simulation::inject(Vec p, Vec v, float volume) {
  if (!std::isfinite(p.x) || !std::isfinite(p.y) || !std::isfinite(v.x) ||
      !std::isfinite(v.y) || !std::isfinite(volume) || volume <= 0 ||
      volume > 10000)
    return false;
  for (auto &f : flakes_)
    if (!f.active) {
      f = {p, v, random(0, 6.283185f), volume, true, false};
      return true;
    }
  ++totals_.pool_dropped;
  return false;
}
bool Simulation::deposit(std::uint64_t id, float x, float volume) {
  auto *e = edge(id);
  if (!e || !std::isfinite(x) || !std::isfinite(volume) || volume < 0 ||
      volume > 10000 || !exposed(e->support, x))
    return false;
  auto i = static_cast<std::size_t>(
      std::clamp((x - e->support.rect.x) / e->support.rect.w, 0.f, .999999f) *
      static_cast<float>(e->count));
  e->cells[i] += volume;
  totals_.deposited += volume;
  return true;
}
bool Simulation::discardSupport(std::uint64_t id) {
  auto *e = edge(id);
  if (!e)
    return false;
  for (auto &v : e->cells) {
    totals_.discarded += v;
    v = 0;
  }
  e->active = false;
  return true;
}
void Simulation::shed(Edge &e, std::size_t i, double amount) {
  amount = std::min(amount, e.cells[i]);
  if (amount <= 0)
    return;
  e.cells[i] -= amount;
  auto r = e.support.rect;
  Vec p{r.x + (static_cast<float>(i) + .5f) * r.w / static_cast<float>(e.count),
        r.y + 1};
  bool allocated = false;
  Flake *replacement = nullptr;
  for (auto &f : flakes_)
    if (!f.active) {
      f = {p,
           {random(-25, 25), random(25, 50)},
           random(0, 6.283185f),
           amount,
           true,
           true};
      allocated = true;
      break;
    } else if (!f.accounted && !replacement)
      replacement = &f;
  // Atmospheric flakes carry no deposited mass. Reclaim one before retiring
  // accumulated snow, even if a close/move temporarily exceeds the reserve.
  if (!allocated && replacement) {
    *replacement = {p,
                    {random(-25, 25), random(25, 50)},
                    random(0, 6.283185f),
                    amount,
                    true,
                    true};
    allocated = true;
    ++totals_.ambient_recycled;
  }
  if (!allocated) {
    ++totals_.pool_dropped;
    totals_.escaped += amount;
  }
  ++totals_.shed;
}
void Simulation::reconcile(std::span<const Support> ss) {
  for (auto &e : edges_)
    if (e.active) {
      const Support *current = nullptr;
      for (const auto &s : ss)
        if (s.id == e.support.id)
          current = &s;
      if (!current) {
        for (std::size_t i = 0; i < e.count; ++i)
          shed(e, i, e.cells[i]);
        e.active = false;
        continue;
      }
      auto a = e.support.rect, b = current->rect;
      float distance = std::hypot(b.x - a.x, b.y - a.y);
      bool abrupt =
          distance > 80 || std::abs(b.w - a.w) > std::max(32.f, a.w * .2f);
      if (abrupt)
        for (std::size_t i = 0; i < e.count; ++i)
          shed(e, i, e.cells[i]);
      else if (distance > 1)
        for (std::size_t i = 0; i < e.count; ++i)
          shed(e, i, e.cells[i] * std::min(.15f, distance * .003f));
      const auto count = static_cast<std::size_t>(std::clamp(
          std::ceil(b.w / 4), 1.f, static_cast<float>(cell_cap)));
      if (count != e.count) {
        remap_cells(e.cells, e.count, count);
        e.count = count;
      }
      e.support = *current;
      for (std::size_t i = 0; i < e.count; ++i)
        if (!exposed(*current, b.x + (static_cast<float>(i) + .5f) * b.w /
                                         static_cast<float>(e.count))) {
          totals_.discarded += e.cells[i];
          e.cells[i] = 0;
        }
    }
  for (const auto &s : ss)
    if (!edge(s.id))
      for (auto &e : edges_)
        if (!e.active) {
          e = {};
          e.active = true;
          e.support = s;
          e.count = static_cast<std::size_t>(std::clamp(
              std::ceil(s.rect.w / 4), 1.f, static_cast<float>(cell_cap)));
          break;
        }
}
Hit Simulation::sweep_top(Vec a, Vec b, std::span<const Support> ss) {
  std::array<Support, physics::support_cap> prepared{};
  if (!std::isfinite(a.x) || !std::isfinite(a.y) || !std::isfinite(b.x) ||
      !std::isfinite(b.y) || !prepare(ss, prepared))
    return {};
  return sweep(a, b, std::span(prepared).first(ss.size()));
}
Metrics Simulation::metrics() const {
  auto m = totals_;
  m.floor_volume = floorSnow();
  m.settled += m.floor_volume;
  for (const auto &e : edges_)
    if (e.active)
      for (double v : e.cells)
        m.settled += v;
  for (const auto &f : flakes_)
    if (f.active) {
      ++m.active;
      if (f.accounted)
        m.falling += f.volume;
    }
  return m;
}
bool Simulation::hasResidualActivity() const {
  for (const auto &f : flakes_) if (f.active) return true;
  for (const auto &e : edges_) if (e.active)
    for (std::size_t i = 0; i < e.count; ++i) if (e.cells[i] > 0) return true;
  for (std::size_t i = 0; i < floor_count_; ++i) if (floor_[i] > 0) return true;
  return false;
}
void Simulation::discardAccumulation() {
  for (auto &e : edges_) if (e.active) discardSupport(e.support.id);
  totals_.discarded += floorSnow();
  floor_ = {};
  // Released flakes own previously deposited mass. Turning accumulation off
  // retires it immediately, so a quick re-enable cannot resurrect old snow.
  // Atmospheric flakes are still visual precipitation and remain independent.
  for (auto &f : flakes_) if (f.active && f.accounted) {
    totals_.discarded += f.volume;
    f = {};
  }
}
bool Simulation::step(float dt, float width, float height, const Parameters &p,
                      std::span<const Support> ss, Frame &frame, physics::InteractionOptions options) {
  frame.segment_count = 0;
  frame.flake_segments = 0;
  frame.masked_segments = 0;
  frame.valid = false;
  if (!p.valid() || !std::isfinite(dt) || dt < 0 || dt > 1 ||
      !std::isfinite(width) || !std::isfinite(height) || width <= 0 ||
      height <= 0 || width > 16384 || height > 16384 ||
      !prepare(ss, topology_)) {
    reset();
    frame.metrics = {};
    return false;
  }
  auto current = std::span(topology_).first(ss.size());
  resizeFloor(width);
  if (!options.accumulation)
    discardAccumulation();
  else if (!options.window_physics)
    for (auto &e : edges_) if (e.active) discardSupport(e.support.id);
  reconcile(current);
  dt = std::min(dt, .05f) * p.speed;
  time_ += dt;
  if (p.temperature_known && p.temperature_c > 0) {
    // A visual thaw rate, not a prediction of physical snow depth or melt time.
    // Unknown/stale temperature is conservative: no inferred warming.
    const double loss = .015 * p.temperature_c * dt;
    for (auto &e : edges_) if (e.active)
      for (std::size_t i = 0; i < e.count; ++i) {
        const double before = e.cells[i];
        e.cells[i] = std::max(0., before - loss);
        totals_.melted += before - e.cells[i];
      }
    const double floorLoss = loss * floor_width_ / static_cast<double>(floor_count_);
    for (std::size_t i = 0; i < floor_count_; ++i) {
      const double amount = std::min(floor_[i], floorLoss);
      floor_[i] -= amount;
      totals_.melted += amount;
    }
  }
  std::size_t ambient = 0, active = 0;
  for (const auto &f : flakes_)
    if (f.active) {
      ++active;
      if (!f.accounted)
        ++ambient;
    }
  // Budget for a full-height residence at the mean downward speed. Cap the
  // atmospheric population separately so deposited material owns 600 slots.
  const float rate =
      std::min(240.f, static_cast<float>(ambient_cap) * 45.f / (height + 12.f));
  spawn_credit_ += dt * p.strength * rate;
  while (spawn_credit_ >= 1) {
    spawn_credit_ -= 1;
    if (ambient >= ambient_cap || active >= flake_cap) {
      ++totals_.atmospheric_throttled;
      continue; // Consume credit; capacity changes must not produce a burst.
    }
    if (inject({random(0, width), -4}, {p.wind, random(25, 65)}, 1)) {
      ++ambient;
      ++active;
    }
  }
  for (auto &f : flakes_)
    if (f.active) {
      auto old = f.p;
      float wave = std::sin(static_cast<float>(time_) * 1.7f + f.phase);
      f.v.x += (p.wind + wave * 22 - f.v.x) * std::min(1.f, dt * 2);
      f.p.x += f.v.x * dt;
      f.p.y += f.v.y * dt;
      auto hit = options.window_physics ? sweep(old, f.p, current) : Hit{};
      float floorTime = 2;
      float floorX = 0;
      if (f.p.y > old.y && old.y <= height && f.p.y >= height) {
        floorTime = (height - old.y) / (f.p.y - old.y);
        floorX = old.x + (f.p.x - old.x) * floorTime;
        if (floorX < 0 || floorX >= width) floorTime = 2;
        // The current frame's window footprint determines exposed floor.
        // Existing snow remains conserved underneath a moved window, but
        // neither snowfall nor floor rendering is allowed through that window.
        for (const auto &s : current)
          if (floorX >= s.rect.x && floorX < s.rect.x + s.rect.w &&
              s.rect.y < height && s.rect.y + s.rect.h >= height)
            floorTime = 2;
      }
      if (floorTime <= 1 && (!hit.hit || floorTime < hit.fraction)) {
        const double volume = options.accumulation ?
            (f.accounted ? f.volume : f.volume * p.accumulation) : 0;
        if (!options.accumulation && f.accounted) totals_.discarded += f.volume;
        receiveFloor(floorX, volume);
        if (!f.accounted) totals_.deposited += volume;
        ++totals_.impacts;
        f.active = false;
      } else if (hit.hit) {
        auto *e = edge(hit.id);
        auto i = static_cast<std::size_t>(
            std::clamp((hit.point.x - e->support.rect.x) / e->support.rect.w,
                       0.f, .999999f) *
            static_cast<float>(e->count));
        const double volume = options.accumulation ? (f.accounted ? f.volume : f.volume * p.accumulation) : 0;
        if (!options.accumulation && f.accounted) totals_.discarded += f.volume;
        e->cells[i] += volume;
        if (!f.accounted)
          totals_.deposited += volume;
        ++totals_.impacts;
        f.active = false;
      } else if (f.p.y > height + 8 || f.p.x < -64 || f.p.x > width + 64) {
        if (f.accounted)
          totals_.escaped += f.volume;
        f.active = false;
      }
    }
  for (auto &e : edges_)
    if (e.active)
      for (std::size_t i = 0; i < e.count; ++i)
        if (e.cells[i] > p.overload)
          shed(e, i, e.cells[i] - p.overload);
  std::size_t dropped = 0;
  auto append = [&](Vec a, Vec b, float w, float alpha) {
    if (frame.segment_count < frame.segments.size())
      frame.segments[frame.segment_count++].values = {a.x,  a.y,  b.x, b.y,  w,
                                                      .92f, .96f, 1.f, alpha};
    else
      ++dropped;
  };
  for (const auto &f : flakes_)
    if (f.active) {
      append({f.p.x - .9f, f.p.y}, {f.p.x + .9f, f.p.y + .8f}, 1.6f, .8f);
    }
  frame.flake_segments = frame.segment_count;
  const float floorCell = floor_width_ / static_cast<float>(floor_count_);
  for (std::size_t i = 0; i < floor_count_; ++i) {
    // Neighbor smoothing changes only the contour, never conserved volume.
    const double value = .5 * floor_[i] + .25 * floor_[i ? i - 1 : i] +
                         .25 * floor_[i + 1 < floor_count_ ? i + 1 : i];
    const float mound = std::min(24.f, static_cast<float>(value / floorCell));
    if (mound <= .001f) continue;
    const float x = (static_cast<float>(i) + .5f) * floorCell;
    append({x, height - mound}, {x, height}, floorCell * mound_column_overlap, .95f);
  }
  frame.masked_segments = frame.segment_count;
  for (const auto &e : edges_)
    if (e.active) {
      auto r = e.support.rect;
      float cell = r.w / static_cast<float>(e.count);
      const float columnWidth = cell * mound_column_overlap;
      auto visibleCell = [&](std::size_t i) {
        const float x = r.x + (static_cast<float>(i) + .5f) * cell;
        const float left = x - columnWidth * .5f, right = x + columnWidth * .5f;
        if (left < r.x + 8 || right > r.x + r.w - 8)
          return false;
        for (std::size_t j = 0; j < e.support.span_count; ++j)
          if (left >= e.support.spans[j].left &&
              right <= e.support.spans[j].right)
            return true;
        return false;
      };
      for (std::size_t i = 0; i < e.count; ++i)
        if (visibleCell(i)) {
          float x = r.x + (static_cast<float>(i) + .5f) * cell;
          // Filter only across contiguous visible cells. This affects the
          // visual contour, never reservoir mass or collision accounting.
          double moundHeight = e.cells[i] * .5;
          if (i && visibleCell(i - 1))
            moundHeight += e.cells[i - 1] * .25f;
          else
            moundHeight += e.cells[i] * .25f;
          if (i + 1 < e.count && visibleCell(i + 1))
            moundHeight += e.cells[i + 1] * .25f;
          else
            moundHeight += e.cells[i] * .25f;
          moundHeight = std::min(18., moundHeight);
          if (moundHeight <= .001f)
            continue;
          const float strokeWidth = columnWidth;
          // The GPU renders flat-ended rectangles without cap extension.
          // Preserve the full vertical height even for subpixel deposits;
          // neighbor filtering supplies the mound silhouette.
          append({x, r.y - static_cast<float>(moundHeight)}, {x, r.y}, strokeWidth, .92f);
        }
    }
  frame.metrics = metrics();
  frame.metrics.segments_dropped = dropped;
  frame.valid = true;
  return true;
}
} // namespace a_weather_app::snow
