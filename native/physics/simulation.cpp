#include "simulation.hpp"
#include <algorithm>
#include <cmath>

namespace a_weather_app::physics {
namespace {
float length(Vec v) {
    return std::hypot(v.x, v.y);
}
Vec sub(Vec a, Vec b) {
    return {a.x - b.x, a.y - b.y};
}
bool finite(float f) {
    return std::isfinite(f);
}
bool rect_valid(Rect r) {
    return finite(r.x) && finite(r.y) && finite(r.w) && finite(r.h) && std::abs(r.x) <= 32768 &&
           std::abs(r.y) <= 32768 && r.w > 0 && r.h > 0 && r.w <= 16384 && r.h <= 16384;
}
bool supports_valid(std::span<const Support> ss) {
    if (ss.size() > support_cap)
        return false;
    for (std::size_t i = 0; i < ss.size(); ++i) {
        auto& s = ss[i];
        if (!s.id || !rect_valid(s.rect) || s.span_count > span_cap)
            return false;
        for (std::size_t j = 0; j < i; ++j)
            if (ss[j].id == s.id)
                return false;
        float last = s.rect.x;
        for (std::size_t j = 0; j < s.span_count; ++j) {
            auto p = s.spans[j];
            if (!finite(p.left) || !finite(p.right) || p.left < last || p.right <= p.left ||
                p.right > s.rect.x + s.rect.w)
                return false;
            last = p.right;
        }
    }
    return true;
}
bool exposed(const Support& s, float x) {
    for (std::size_t j = 0; j < s.span_count; ++j)
        if (x >= s.spans[j].left && x < s.spans[j].right)
            return true;
    return false;
}
bool floor_exposed(float x, float y, std::span<const Support> ss) {
    for (const auto& s : ss)
        if (x >= s.rect.x && x < s.rect.x + s.rect.w && y >= s.rect.y && y < s.rect.y + s.rect.h)
            return false;
    return true;
}
// Conservative normalized-area remapping, including large width changes.
template <std::size_t N>
void remap(std::array<double, N>& cells, std::size_t old_count, std::size_t count) {
    std::array<double, N> next{};
    if (old_count)
        for (std::size_t i = 0; i < old_count; ++i) {
            const double begin = static_cast<double>(i) / static_cast<double>(old_count);
            const double end = static_cast<double>(i + 1) / static_cast<double>(old_count);
            const auto first =
                std::min(count - 1, static_cast<std::size_t>(begin * static_cast<double>(count)));
            const auto last =
                std::min(count - 1, static_cast<std::size_t>(end * static_cast<double>(count)));
            double remaining = cells[i];
            for (std::size_t j = first; j <= last; ++j) {
                const double overlap = std::max(
                    0., std::min(end, static_cast<double>(j + 1) / static_cast<double>(count)) -
                            std::max(begin, static_cast<double>(j) / static_cast<double>(count)));
                const double amount =
                    j == last
                        ? remaining
                        : std::min(remaining, cells[i] * overlap * static_cast<double>(old_count));
                next[j] += amount;
                remaining -= amount;
            }
        }
    cells = next;
}
void append(Frame& f, Vec a, Vec b, float width, float r, float g, float blue, float alpha) {
    if (f.segment_count == segment_cap) {
        ++f.metrics.segments_dropped;
        return;
    }
    f.segments[f.segment_count++].values = {a.x, a.y, b.x, b.y, width, r, g, blue, alpha};
}
void streak(Frame& f, Vec p, Vec v, float l, float w, float alpha) {
    float norm = length(v);
    Vec half = norm > 0 ? Vec{v.x / norm * l * .5f, v.y / norm * l * .5f} : Vec{0, l * .5f};
    append(f, sub(p, half), {p.x + half.x, p.y + half.y}, w, .73f, .85f, .94f, alpha);
}
// Clip supplied topology once, so stale/untrusted exposure cannot create an
// impact behind a foreground window. No rectangle-pair work per particle.
bool topology(std::span<const Support> input, std::span<Support> output) {
    for (std::size_t i = 0; i < input.size(); ++i) {
        output[i] = input[i];
        auto& s = output[i];
        for (std::size_t j = 0; j < input.size(); ++j) {
            const auto& o = input[j];
            if (i == j || !(o.stack > s.stack || (o.stack == s.stack && j > i)) ||
                o.rect.y > s.rect.y || o.rect.y + o.rect.h <= s.rect.y)
                continue;
            std::array<Span, span_cap> next{};
            std::size_t count = 0;
            auto add = [&](float left, float right) {
                if (right <= left)
                    return true;
                if (count == span_cap)
                    return false;
                next[count++] = {left, right};
                return true;
            };
            for (std::size_t k = 0; k < s.span_count; ++k) {
                auto p = s.spans[k];
                if (o.rect.x + o.rect.w <= p.left || o.rect.x >= p.right) {
                    if (!add(p.left, p.right))
                        return false;
                } else {
                    if (!add(p.left, std::min(p.right, o.rect.x)) ||
                        !add(std::max(p.left, o.rect.x + o.rect.w), p.right))
                        return false;
                }
            }
            s.spans = next;
            s.span_count = count;
        }
    }
    return true;
}
Hit sweep(Vec a, Vec b, std::span<const Support> ss) {
    Hit result{};
    float best = 2;
    if (b.y <= a.y)
        return result;
    for (const auto& s : ss) {
        float t = (s.rect.y - a.y) / (b.y - a.y);
        if (t < 0 || t > 1 || t >= best)
            continue;
        float x = a.x + (b.x - a.x) * t;
        if (x < s.rect.x + 8 || x > s.rect.x + s.rect.w - 8 || !exposed(s, x))
            continue;
        best = t;
        result = {true, s.id, {x, s.rect.y}, t};
    }
    return result;
}
} // namespace
bool Parameters::valid() const {
    const float v[] = {rain_intensity,   wind_x,           wind_y,
                       gravity,          far_density,      mid_density,
                       near_density,     drop_length,      drop_width,
                       drop_opacity,     drop_speed,       splash_amount,
                       splash_lifetime,  splash_velocity,  water_accumulation_rate,
                       runoff_threshold, evaporation_rate, simulation_speed};
    const float lo[] = {0, -500, -200, 0, 0, 0, 0, .25f, .25f, 0, .2f, 0, .05f, 10, 0, .1f, 0, 0};
    const float hi[] = {1, 500, 200, 400, 2, 2, 2, 3, 2, 1, 2.5f, 8, 1, 300, 5, 10, 2, 2};
    for (std::size_t i = 0; i < 18; ++i)
        if (!finite(v[i]) || v[i] < lo[i] || v[i] > hi[i])
            return false;
    return true;
}
float Simulation::random(float a, float b) {
    rng_ ^= rng_ >> 12;
    rng_ ^= rng_ << 25;
    rng_ ^= rng_ >> 27;
    auto v = rng_ * 2685821657736338717ULL;
    return a + (b - a) * static_cast<float>(v >> 40) / 16777216.f;
}
void Simulation::reset(std::uint64_t seed) {
    rng_ = seed ? seed : 92817;
    serial_ = 0;
    step_serial_ = 0;
    clock_ = 0;
    metrics_ = {};
    floor_ = {};
    floor_count_ = 0;
    width_ = height_ = 0;
    for (auto& e : edges_)
        e = Edge{};
    for (auto& p : splashes_)
        p = {};
    for (auto& p : runoff_)
        p = {};
    for (std::size_t i = 0; i < drops_.size(); ++i)
        drops_[i] = {{random(-500, 1800), random(-900, 800)}, {0, i >= 1500 ? 780.f : 510.f}, 0, 0};
}
Simulation::Edge* Simulation::edge(std::uint64_t id) {
    for (auto& e : edges_)
        if (e.active && e.id == id)
            return &e;
    return nullptr;
}
Hit Simulation::sweep_top(Vec a, Vec b, std::span<const Support> ss) {
    std::array<Support, support_cap> prepared{};
    if (!finite(a.x) || !finite(a.y) || !finite(b.x) || !finite(b.y) || !supports_valid(ss) ||
        !topology(ss, std::span(prepared).first(ss.size())))
        return {};
    return sweep(a, b, std::span(prepared).first(ss.size()));
}
bool Simulation::deposit(std::uint64_t id, float x, float volume) {
    auto* e = edge(id);
    if (!e || !finite(x) || x < e->rect.x || x > e->rect.x + e->rect.w || !finite(volume) ||
        volume < 0 || volume > 10000)
        return false;
    metrics_.deposited += volume;
    credit(*e, x, volume);
    return true;
}
void Simulation::credit(Edge& e, float x, double volume) {
    auto i = static_cast<std::size_t>(std::clamp((x - e.rect.x) / e.rect.w, 0.f, .999999f) *
                                      static_cast<float>(e.count));
    const double accepted = std::min(volume, std::max(0., 8 - e.cells[i]));
    e.cells[i] += accepted;
    if (volume > accepted)
        spawn(e, static_cast<float>(i), volume - accepted);
}
bool Simulation::packet(Vec position, Vec velocity, double volume, std::uint64_t emitter_id,
                        bool right_outlet) {
    if (emitter_id)
        for (auto& p : runoff_)
            if (p.volume > 0 && p.emitter_id == emitter_id && p.right_outlet == right_outlet &&
                p.ready_step == step_serial_ + 1 && p.age == 0 && p.p.x == position.x &&
                p.p.y == position.y) {
                // A saturated bank may receive many small impacts in one frame. They
                // leave the same physical outlet together, not as hundreds of slots.
                // Preserve mass and momentum without aging or deferring either share.
                const double combined = p.volume + volume;
                const double weight = volume / combined;
                p.v.x = static_cast<float>(p.v.x + (velocity.x - p.v.x) * weight);
                p.v.y = static_cast<float>(p.v.y + (velocity.y - p.v.y) * weight);
                p.volume = combined;
                return true;
            }
    for (auto& p : runoff_)
        if (p.volume == 0) {
            p = {position, velocity, volume, 0, step_serial_ + 1, emitter_id, right_outlet};
            ++metrics_.runoff_total;
            return true;
        }
    return false;
}
bool Simulation::injectRunoff(Vec position, Vec velocity, float volume) {
    if (!floor_count_ || !finite(position.x) || !finite(position.y) || !finite(velocity.x) ||
        !finite(velocity.y) || !finite(volume) || position.x < 0 || position.x >= width_ ||
        position.y > height_ || position.y < -32768 || std::abs(velocity.x) > 2000 ||
        velocity.y < 0 || velocity.y > 2000 || volume <= 0 || volume > 10000)
        return false;
    if (!packet(position, velocity, volume))
        return false;
    metrics_.deposited += volume;
    return true;
}
bool Simulation::depositFloor(float x, float volume) {
    if (!floor_count_ || !finite(x) || !finite(volume) || x < 0 || x >= width_ || volume < 0 ||
        volume > 10000)
        return false;
    metrics_.deposited += volume;
    floorCredit(x, volume);
    return true;
}
void Simulation::floorCredit(float x, double volume) {
    // Detached water can start outside the output after a close or move. Never
    // convert a negative coordinate to an unsigned index, even on a floor/exit
    // tie. This is accepted internal mass, so give it an explicit terminal sink.
    if (!floor_count_ || !finite(x) || x < 0 || x >= width_) {
        metrics_.escaped += volume;
        return;
    }
    const auto i = std::min(
        floor_count_ - 1, static_cast<std::size_t>(x / width_ * static_cast<float>(floor_count_)));
    const double amount = std::min(volume, std::max(0., floor_cell_capacity - floor_[i]));
    floor_[i] += amount;
    metrics_.overflow += volume - amount;
}
double Simulation::supportVolume(std::uint64_t id) const {
    for (const auto& e : edges_)
        if (e.active && e.id == id) {
            double volume = 0;
            for (std::size_t i = 0; i < e.count; ++i)
                volume += e.cells[i];
            return volume;
        }
    return 0;
}
void Simulation::floorLayout(float width, float height) {
    const auto count =
        std::clamp(static_cast<std::size_t>(width / 16), std::size_t{1}, floor_cell_cap);
    if (count != floor_count_)
        remap(floor_, floor_count_, count);
    floor_count_ = count;
    width_ = width;
    height_ = height;
    for (std::size_t i = 0; i < count; ++i) {
        const double excess = std::max(0., floor_[i] - floor_cell_capacity);
        floor_[i] -= excess;
        metrics_.overflow += excess;
    }
}
bool Simulation::discardSupport(std::uint64_t id) {
    auto* e = edge(id);
    if (!e)
        return false;
    for (std::size_t i = 0; i < e->count; ++i)
        metrics_.discarded += e->cells[i];
    // Already falling packets remain independent of this support.
    *e = Edge{};
    return true;
}
void Simulation::spawn(const Edge& e, float cell, double volume, bool detached) {
    if (volume <= 0)
        return;
    const bool left = cell < static_cast<float>(e.count) * .5f;
    const float x = detached ? e.rect.x + (cell + .5f) / static_cast<float>(e.count) * e.rect.w
                             : (left ? e.rect.x - .5f : e.rect.x + e.rect.w + .5f);
    const Vec velocity{detached ? random(-32, 32) : (left ? -8.f : 8.f) + random(-5, 5),
                       random(22, detached ? 105.f : 48.f) +
                           static_cast<float>(std::min(volume * 20, 80.))};
    metrics_.transferred += volume;
    if (!packet({x, e.rect.y + .05f}, velocity, volume, detached ? 0 : e.id, !left)) {
        metrics_.overflow += volume;
        ++metrics_.runoff_dropped;
    }
}
void Simulation::detach(Edge& e, float fraction) {
    double volume = 0;
    std::array<double, cell_cap> cumulative{};
    for (std::size_t i = 0; i < e.count; ++i) {
        double removed = e.cells[i] * fraction;
        e.cells[i] -= removed;
        volume += removed;
        cumulative[i] = volume;
    }
    metrics_.detached += volume;
    if (volume <= 0)
        return;
    int packets = std::clamp(static_cast<int>(std::ceil(volume / 1.5f)), 1, 12);
    for (int i = 0; i < packets; ++i) {
        // Sample the actual removed water, rather than a regular grid spanning dry
        // cells too. Fractional cell placement avoids quantized packet positions.
        double sample = random(0, 1) * volume;
        std::size_t cell = 0;
        while (cell + 1 < e.count && cumulative[cell] <= sample)
            ++cell;
        float position = std::clamp(static_cast<float>(cell) + random(-.45f, .45f), 0.f,
                                    static_cast<float>(e.count - 1));
        spawn(e, position, volume / static_cast<double>(packets), true);
    }
}
void Simulation::reconcile(std::span<const Support> ss) {
    for (auto& e : edges_)
        if (e.active) {
            bool found = false;
            for (auto& s : ss)
                if (s.id == e.id)
                    found = true;
            if (!found) {
                detach(e, 1);
                e.active = false;
            }
        }
    for (auto& s : ss) {
        auto* e = edge(s.id);
        std::size_t count =
            std::clamp(static_cast<std::size_t>(s.rect.w / 16), std::size_t{2}, cell_cap);
        if (!e) {
            std::size_t existing = 0;
            for (const auto& candidate : edges_)
                if (candidate.active)
                    ++existing;
            for (auto& candidate : edges_)
                if (!candidate.active) {
                    e = &candidate;
                    break;
                }
            *e = Edge{};
            e->active = true;
            e->id = s.id;
            e->rect = s.rect;
            e->origin = {s.rect.x, s.rect.y};
            e->origin_width = s.rect.w;
            e->last_change = clock_;
            e->cooldown = static_cast<float>(existing) * .19f;
            e->count = count;
            continue;
        }
        Vec movement{s.rect.x - e->rect.x, s.rect.y - e->rect.y};
        bool changed = length(movement) > .01f ||
                       std::hypot(s.rect.w - e->rect.w, s.rect.h - e->rect.h) > .01f;
        if (changed) {
            if (clock_ - e->last_change >= .2) {
                e->origin = {e->rect.x, e->rect.y};
                e->origin_width = e->rect.w;
                e->episode_detached = false;
            }
            e->last_change = clock_;
        }
        bool abrupt = length(sub({s.rect.x, s.rect.y}, e->origin)) > 90 ||
                      std::abs(s.rect.w - e->origin_width) > e->origin_width * .3f;
        if (abrupt && !e->episode_detached) {
            detach(*e, .45f);
            e->episode_detached = true;
        } else {
            e->lag.x -= movement.x * .15f;
            e->lag.y -= movement.y * .15f;
            float l = length(e->lag);
            if (l > 7) {
                e->lag.x *= 7 / l;
                e->lag.y *= 7 / l;
            }
        }
        if (count != e->count) {
            remap(e->cells, e->count, count);
            e->count = count;
        }
        e->rect = s.rect;
    }
}
void Simulation::splash(Vec point, const Parameters& p) {
    if (p.splash_amount <= 0)
        return;
    int count = std::clamp(static_cast<int>(p.splash_amount * (.5f + p.rain_intensity)), 2, 8);
    for (int i = 0; i < count; ++i) {
        for (auto& part : splashes_)
            if (part.life <= 0) {
                part.p = point;
                part.v = {random(-.8f, .8f) * p.splash_velocity + p.wind_x * .12f,
                          -random(.35f, 1) * p.splash_velocity};
                part.life = part.total = p.splash_lifetime * random(.7f, 1.2f);
                break;
            }
    }
}
void Simulation::floorStep(float dt, const Parameters& p) {
    auto next = floor_;
    const double factor = std::min(.24, static_cast<double>(dt) * 4);
    for (std::size_t i = 0; i + 1 < floor_count_; ++i) {
        const double flux = (floor_[i] - floor_[i + 1]) * factor;
        next[i] -= flux;
        next[i + 1] += flux;
    }
    for (std::size_t i = 0; i < floor_count_; ++i) {
        const double drained = std::min(next[i], floor_drain_rate * dt);
        next[i] -= drained;
        metrics_.drained += drained;
        const double evaporated =
            std::min(next[i], static_cast<double>(p.evaporation_rate) * dt * .025);
        next[i] -= evaporated;
        metrics_.evaporated += evaporated;
    }
    floor_ = next;
}
void Simulation::water(float dt, const Parameters& p, std::span<const Support> ss,
                       bool window_physics) {
    // Advance only packets that existed at the start of this frame. A transfer
    // can emit capacity overflow, but that new packet never consumes dt twice.
    const float floor_y = height_ - .5f;
    for (auto& part : runoff_) {
        if (part.volume <= 0 || part.ready_step > step_serial_ || dt == 0)
            continue;
        part.age += dt;
        part.v.y = std::min(2000.f, part.v.y + 330 * dt);
        const Vec end{part.p.x + part.v.x * dt, part.p.y + part.v.y * dt};
        const Hit hit = window_physics ? sweep(part.p, end, ss) : Hit{};
        const float floor_t = end.y > part.p.y ? (floor_y - part.p.y) / (end.y - part.p.y) : 2;
        const bool floor_hit = floor_t >= 0 && floor_t <= 1 && (!hit.hit || floor_t < hit.fraction);
        const float t = floor_hit ? floor_t : hit.hit ? hit.fraction : 2;
        float exit_t = 2;
        if (part.p.x < 0 || part.p.x >= width_ || part.p.y > floor_y)
            exit_t = 0;
        else if (end.x < 0)
            exit_t = -part.p.x / (end.x - part.p.x);
        else if (end.x >= width_)
            exit_t = (width_ - part.p.x) / (end.x - part.p.x);
        if ((exit_t <= 1 && exit_t <= t) || (t > 1 && part.age >= 12)) {
            metrics_.escaped += part.volume;
            part.volume = 0;
        } else if (floor_hit) {
            const float x = part.p.x + (end.x - part.p.x) * floor_t;
            const double volume = part.volume;
            part.volume = 0; // Ownership ends before another store receives the mass.
            if (floor_exposed(x, floor_y, ss)) {
                floorCredit(x, volume);
                ++metrics_.floor_impacts;
            } else
                metrics_.discarded += volume;
        } else if (hit.hit) {
            const double volume = part.volume;
            part.volume = 0;
            if (auto* target = edge(hit.id)) {
                credit(*target, hit.point.x, volume);
                ++metrics_.lower_impacts;
            } else
                metrics_.discarded += volume; // Defensive: topology and stores must agree.
        } else
            part.p = end;
    }
    for (auto& e : edges_)
        if (e.active) {
            const float decay = std::exp(-dt * 4);
            e.lag.x *= decay;
            e.lag.y *= decay;
            auto next = e.cells;
            const double factor = std::min(.2, static_cast<double>(dt) * 1.5);
            for (std::size_t i = 0; i + 1 < e.count; ++i) {
                const double flux = (e.cells[i] - e.cells[i + 1]) * factor;
                next[i] -= flux;
                next[i + 1] += flux;
            }
            for (std::size_t i = 1; i + 1 < e.count; ++i) {
                const auto destination = i < e.count / 2 ? i - 1 : i + 1;
                const double flow = next[i] * std::min(1., static_cast<double>(dt) * .12);
                next[i] -= flow;
                next[destination] += flow;
            }
            e.cooldown = std::max(0.f, e.cooldown - dt);
            bool spilled = false;
            for (std::size_t i = 0; i < e.count; ++i) {
                const double evaporated =
                    std::min(next[i], static_cast<double>(p.evaporation_rate) * dt * .025);
                next[i] -= evaporated;
                metrics_.evaporated += evaporated;
                double removed = std::max(0., next[i] - 8);
                next[i] -= removed;
                if (dt > 0 && (i == 0 || i + 1 == e.count) && next[i] >= p.runoff_threshold &&
                    e.cooldown <= 0) {
                    const double spill = std::min(next[i], .35 + next[i] * .15);
                    next[i] -= spill;
                    removed += spill;
                    spilled = true;
                }
                if (removed > 0)
                    spawn(e, static_cast<float>(i), removed);
            }
            if (spilled) {
                ++serial_;
                e.cooldown = .28f + std::fmod(static_cast<float>(serial_) * .371f, .52f);
            }
            e.cells = next;
        }
    floorStep(dt, p);
}
Metrics Simulation::metrics() const {
    auto m = metrics_;
    m.water_volume = 0;
    m.floor_volume = m.runoff_volume = 0;
    m.splash_active = 0;
    m.runoff_active = 0;
    for (auto& e : edges_)
        if (e.active)
            for (std::size_t i = 0; i < e.count; ++i)
                m.water_volume += e.cells[i];
    for (auto& p : splashes_)
        if (p.life > 0)
            ++m.splash_active;
    for (auto& p : runoff_)
        if (p.volume > 0) {
            ++m.runoff_active;
            m.runoff_volume += p.volume;
        }
    for (std::size_t i = 0; i < floor_count_; ++i)
        m.floor_volume += floor_[i];
    return m;
}
bool Simulation::hasResidualActivity() const {
    for (const auto& p : splashes_)
        if (p.life > 0)
            return true;
    for (const auto& p : runoff_)
        if (p.volume > 0)
            return true;
    for (const auto& e : edges_)
        if (e.active)
            for (std::size_t i = 0; i < e.count; ++i)
                if (e.cells[i] > 0)
                    return true;
    for (std::size_t i = 0; i < floor_count_; ++i)
        if (floor_[i] > 0)
            return true;
    return false;
}
void Simulation::draw(const Parameters& p, std::span<const Support> ss, Frame& f) const {
    int mid = static_cast<int>(1500 * std::clamp(p.rain_intensity * p.mid_density, 0.f, 1.f)),
        near = static_cast<int>(420 * std::clamp(p.rain_intensity * p.near_density, 0.f, 1.f));
    for (int i = 0; i < mid; ++i) {
        auto& d = drops_[static_cast<std::size_t>(i)];
        streak(f, d.p, d.v, 17 * p.drop_length, .9f * p.drop_width, .3f * p.drop_opacity);
    }
    for (int i = 0; i < near; ++i) {
        auto& d = drops_[static_cast<std::size_t>(i + 1500)];
        streak(f, d.p, d.v, 31 * p.drop_length, 1.35f * p.drop_width, .43f * p.drop_opacity);
    }
    f.rain_segments = f.segment_count;
    // All free water and floor columns share the current rectangle mask, unlike
    // exposed window-edge water, which is clipped to prepared top spans below.
    for (const auto& d : runoff_)
        if (d.volume > 0) {
            const float amount = static_cast<float>(std::min(d.volume, 8.));
            streak(f, d.p, d.v, 6 + amount, .8f + amount * .14f, .45f);
        }
    for (std::size_t i = 0; i < floor_count_; ++i)
        if (floor_[i] > .015) {
            const float cell_width = width_ / static_cast<float>(floor_count_);
            const float x = (static_cast<float>(i) + .5f) * cell_width;
            const float depth = static_cast<float>(std::min(8., floor_[i] * .55));
            append(f, {x, height_ - .5f}, {x, height_ - .5f - depth}, cell_width * 1.55f, .42f,
                   .65f, .79f, .32f);
        }
    f.masked_segments = f.segment_count;
    for (auto& d : splashes_)
        if (d.life > 0)
            streak(f, d.p, d.v, 3, 1, std::min(.6f, d.life / d.total) * p.drop_opacity);
    for (auto& s : ss) {
        const Edge* e = nullptr;
        for (auto& candidate : edges_)
            if (candidate.active && candidate.id == s.id)
                e = &candidate;
        if (!e)
            continue;
        for (std::size_t i = 0; i + 1 < e->count; ++i) {
            float volume = static_cast<float>((e->cells[i] + e->cells[i + 1]) * .5);
            if (volume < .015f)
                continue;
            float x = e->rect.x + e->lag.x +
                      static_cast<float>(i) / static_cast<float>(e->count - 1) * e->rect.w,
                  end = e->rect.x + e->lag.x +
                        static_cast<float>(i + 1) / static_cast<float>(e->count - 1) * e->rect.w;
            float amplitude = std::min(volume * .12f, .5f),
                  opacity = std::min(.5f, .12f + volume * .06f);
            float y = e->rect.y + e->lag.y - .7f;
            for (std::size_t j = 0; j < s.span_count; ++j) {
                float left = std::max(x, s.spans[j].left), right = std::min(end, s.spans[j].right);
                if (right <= left)
                    continue;
                append(f,
                       {left, y - std::sin(static_cast<float>(i) * 1.73f +
                                           static_cast<float>(clock_) * .35f) *
                                      amplitude},
                       {right, y - std::sin(static_cast<float>(i + 1) * 1.73f +
                                            static_cast<float>(clock_) * .35f) *
                                       amplitude},
                       std::min(2.8f, .6f + volume * .25f), .55f, .76f, .85f, opacity);
                if (i % 3 == 0)
                    append(f, {left, e->rect.y + e->lag.y - 1},
                           {std::min(left + 4, right), e->rect.y + e->lag.y - 1.3f}, .5f, .9f, .97f,
                           1, opacity * .6f);
            }
        }
    }
}
void Simulation::discardAccumulation() {
    for (auto& e : edges_)
        if (e.active)
            discardSupport(e.id);
    for (auto& p : runoff_) {
        metrics_.discarded += p.volume;
        p = {};
    }
    for (auto& v : floor_) {
        metrics_.discarded += v;
        v = 0;
    }
}
bool Simulation::step(float delta, float width, float height, const Parameters& p,
                      std::span<const Support> ss, Frame& f, InteractionOptions options) {
    f.segment_count = f.rain_segments = f.masked_segments = f.mask_count = 0;
    f.far = {};
    f.valid = false;
    f.metrics = {};
    if (!finite(delta) || delta < 0 || !finite(width) || !finite(height) || width < 1 ||
        height < 1 || width > 16384 || height > 16384 || !p.valid() || !supports_valid(ss) ||
        !topology(ss, std::span(topology_).first(ss.size()))) {
        reset();
        f.metrics = {};
        f.time = 0;
        return false;
    }
    ss = std::span(topology_).first(ss.size());
    floorLayout(width, height);
    ++step_serial_;
    if (!options.accumulation)
        discardAccumulation();
    else if (!options.window_physics)
        // Window interaction is independent of exposed desktop accumulation.
        // Existing free packets continue toward the floor in world coordinates.
        for (auto& e : edges_)
            if (e.active)
                discardSupport(e.id);
    reconcile(ss);
    float dt = std::min(delta, .1f) * p.simulation_speed;
    for (std::size_t i = 0; i < drops_.size(); ++i) {
        bool near = i >= 1500;
        int count = static_cast<int>(
            (near ? 420 : 1500) *
            std::clamp(p.rain_intensity * (near ? p.near_density : p.mid_density), 0.f, 1.f));
        if (static_cast<int>(near ? i - 1500 : i) >= count)
            continue;
        auto& d = drops_[i];
        float factor = 1 - std::exp(-dt * .7f);
        d.v.x += (p.wind_x * .5f - d.v.x) * factor + p.wind_x * dt;
        d.v.y += ((near ? 780 : 510) * p.drop_speed + p.wind_y - d.v.y) * factor + p.gravity * dt;
        Vec b{d.p.x + d.v.x * dt, d.p.y + d.v.y * dt};
        auto hit = options.window_physics ? sweep(d.p, b, ss) : Hit{};
        const float floor_y = height - .5f;
        const float floor_t = b.y > d.p.y ? (floor_y - d.p.y) / (b.y - d.p.y) : 2;
        const bool floor_hit = floor_t >= 0 && floor_t <= 1 && (!hit.hit || floor_t < hit.fraction);
        if (floor_hit)
            hit = {}; // Floor is encountered before any off-output support.
        if (hit.hit) {
            ++metrics_.impacts;
            if (options.accumulation)
                deposit(hit.id, hit.point.x, .075f * p.water_accumulation_rate);
            splash(hit.point, p);
        }
        if (floor_hit && options.accumulation) {
            const float x = d.p.x + (b.x - d.p.x) * floor_t;
            if (x >= 0 && x < width && floor_exposed(x, floor_y, ss)) {
                depositFloor(x, .075f * p.water_accumulation_rate);
                ++metrics_.floor_impacts;
            }
        }
        if (hit.hit || floor_hit || b.y > height + 80 || b.x < -700 || b.x > width + 700)
            b = {random(-400, width + 400), -random(10, 300)};
        d.p = b;
    }
    for (auto& d : splashes_)
        if (d.life > 0) {
            d.life -= dt;
            if (d.life > 0) {
                d.v.y += (p.gravity + 300) * dt;
                d.p.x += d.v.x * dt;
                d.p.y += d.v.y * dt;
            }
        }
    clock_ += dt;
    water(dt, p, ss, options.window_physics);
    draw(p, ss, f);
    for (auto& s : ss)
        f.masks[f.mask_count++] = s.rect;
    f.far = {static_cast<int>(2400 * std::clamp(p.rain_intensity * p.far_density, 0.f, 1.f)),
             290 * p.drop_speed,
             p.wind_x,
             7 * p.drop_length,
             .65f * p.drop_width,
             .12f * p.drop_opacity};
    auto dropped = f.metrics.segments_dropped;
    f.metrics = metrics();
    f.metrics.segments_dropped = dropped;
    f.time = clock_;
    f.valid = true;
    return true;
}
} // namespace a_weather_app::physics
