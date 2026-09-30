#pragma once
#include <array>
#include <cstddef>
#include <cstdint>
#include <span>

namespace a_weather_app::physics {
constexpr std::size_t support_cap = 64, span_cap = 65, segment_cap = 6000;
constexpr std::size_t runoff_cap = 512, floor_cell_cap = 1024;
constexpr double floor_cell_capacity = 12, floor_drain_rate = .12;
struct Vec {
  float x{}, y{};
};
struct Rect {
  float x{}, y{}, w{}, h{};
};
struct Span {
  float left{}, right{};
};
// Stable nonzero ID; spans are current-frame absolute logical coordinates.
// Stack order increases towards foreground; equal stacks use input order.
struct Support {
  std::uint64_t id{};
  Rect rect{};
  int stack{};
  std::array<Span, span_cap> spans{};
  std::size_t span_count{};
};
struct Parameters {
  float rain_intensity = .6f, wind_x = 35, wind_y = 0, gravity = 70;
  float far_density = 1, mid_density = 1, near_density = 1, drop_length = 1;
  float drop_width = 1, drop_opacity = .65f, drop_speed = 1, splash_amount = 4;
  float splash_lifetime = .3f, splash_velocity = 90,
        water_accumulation_rate = 1;
  float runoff_threshold = 3, evaporation_rate = .1f, simulation_speed = 1;
  bool valid() const;
};
struct Segment {
  std::array<float, 9> values{};
};
struct Far {
  int count{};
  float speed{}, wind{}, length{}, width{}, opacity{};
};
struct Metrics {
  std::uint64_t impacts{}, runoff_total{};
  std::size_t splash_active{}, runoff_active{};
  std::size_t segments_dropped{};
  // External input = all three live stores + terminal outputs. Detached and
  // transferred are internal counters, never additional mass or terminal loss.
  double deposited{}, drained{}, evaporated{}, detached{}, discarded{}, water_volume{};
  double runoff_volume{}, floor_volume{}, escaped{}, overflow{}, transferred{};
  std::size_t runoff_dropped{};
  std::uint64_t lower_impacts{}, floor_impacts{};
};
struct Frame {
  std::array<Segment, segment_cap> segments{};
  std::array<Rect, support_cap> masks{};
  std::size_t segment_count{}, rain_segments{}, masked_segments{}, mask_count{};
  Far far{};
  Metrics metrics{};
  double time{};
  bool valid{};
};
struct Hit {
  bool hit{};
  std::uint64_t id{};
  Vec point{};
  float fraction{};
};
struct InteractionOptions {
  bool window_physics = true, accumulation = true;
};
// No allocations during step. Own one Simulation per monitor, preferably on
// heap. Invalid input clears all supported/transient state and returns an empty
// frame. Context/workspace/suppression changes must call reset (never model
// them as close).
class Simulation {
public:
  explicit Simulation(std::uint64_t seed = 92817) { reset(seed); }
  void reset(std::uint64_t seed = 92817);
  bool step(float delta, float width, float height, const Parameters &,
            std::span<const Support>, Frame &, InteractionOptions = {});
  static Hit sweep_top(Vec, Vec, std::span<const Support>);
  // Explicit impact injection is useful for deterministic reservoirs/tests.
  bool deposit(std::uint64_t id, float x, float volume);
  // External bounded input; a full packet pool rejects without accepting mass.
  bool injectRunoff(Vec position, Vec velocity, float volume);
  bool depositFloor(float x, float volume);
  double supportVolume(std::uint64_t id) const;
  std::span<const double> floorWater() const { return std::span(floor_).first(floor_count_); }
  // Remove only the reservoir, recording discarded volume without new runoff.
  // Use before step when an ID remains mapped but leaves visible geometry.
  bool discardSupport(std::uint64_t id);
  void discardAccumulation();
  Metrics metrics() const;
  // Exact live state, including subpixel stores that still drain/evaporate.
  // Rain streaks are driven by parameter counts rather than particle lifetime.
  bool hasResidualActivity() const;

private:
  static constexpr std::size_t cell_cap = 1024;
  struct Particle {
    Vec p{}, v{};
    float life{}, total{};
  };
  struct Packet {
    Vec p{}, v{};
    double volume{};
    float age{};
    std::uint64_t ready_step{};
    // Only same-frame corner emissions may combine. Detached/injected packets
    // have emitter_id=0 and always preserve their independent trajectories.
    std::uint64_t emitter_id{};
    bool right_outlet{};
  };
  struct Edge {
    bool active{}, episode_detached{};
    std::uint64_t id{};
    Rect rect{};
    std::array<double, cell_cap> cells{};
    std::size_t count{};
    Vec origin{}, lag{};
    float origin_width{}, cooldown{};
    double last_change{};
  };
  std::array<Particle, 1920> drops_{};
  std::array<Particle, 2048> splashes_{};
  std::array<Packet, runoff_cap> runoff_{};
  std::array<double, floor_cell_cap> floor_{};
  std::size_t floor_count_{};
  float width_{}, height_{};
  std::array<Edge, support_cap> edges_{};
  std::array<Support, support_cap> topology_{};
  std::uint64_t rng_{}, serial_{}, step_serial_{};
  double clock_{};
  Metrics metrics_{};
  float random(float, float);
  Edge *edge(std::uint64_t);
  void detach(Edge &, float);
  void spawn(const Edge &, float cell, double volume, bool detached = false);
  bool packet(Vec, Vec, double, std::uint64_t emitter_id = 0, bool right_outlet = false);
  void credit(Edge &, float x, double);
  void floorCredit(float x, double);
  void floorLayout(float width, float height);
  void floorStep(float, const Parameters &);
  void splash(Vec, const Parameters &);
  void reconcile(std::span<const Support>);
  void water(float, const Parameters &, std::span<const Support>, bool window_physics);
  void draw(const Parameters &, std::span<const Support>, Frame &) const;
};
} // namespace a_weather_app::physics
