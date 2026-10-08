.pragma library

// Interpolate vector components, never bearings (359° and 1° are both north).
// Coordinates are projected from the provider's actual, possibly snapped cells.
function vector(speed, from) {
    const radians = from * Math.PI / 180;
    return {
        x: -Math.sin(radians) * speed,
        y: Math.cos(radians) * speed
    };
}
function interpolate(samples, x, y) {
    let vx = 0, vy = 0, weights = 0;
    for (const sample of samples) {
        const dx = x - sample.x, dy = y - sample.y;
        const distance = dx * dx + dy * dy;
        if (distance < 0.0001)
            return {
                x: sample.vx,
                y: sample.vy
            };
        const weight = 1 / distance;
        vx += sample.vx * weight;
        vy += sample.vy * weight;
        weights += weight;
    }
    return weights ? {
        x: vx / weights,
        y: vy / weights
    } : {
        x: 0,
        y: 0
    };
}
function build(samples, width, height) {
    // Duplicate snapped model cells must not gain extra interpolation weight.
    const unique = [];
    for (const sample of samples) {
        if (!unique.some(other => Math.abs(other.x - sample.x) < 0.01 && Math.abs(other.y - sample.y) < 0.01))
            unique.push(sample);
    }
    const columns = 33, rows = 25, values = [];
    for (let row = 0; row < rows; ++row)
        for (let col = 0; col < columns; ++col)
            values.push(interpolate(unique, col * width / (columns - 1), row * height / (rows - 1)));
    return {
        columns: columns,
        rows: rows,
        width: width,
        height: height,
        values: values,
        samples: unique
    };
}
function sample(field, x, y) {
    if (!field || field.width <= 0 || field.height <= 0)
        return {
            x: 0,
            y: 0
        };
    const gx = Math.max(0, Math.min(field.columns - 1.001, x / field.width * (field.columns - 1)));
    const gy = Math.max(0, Math.min(field.rows - 1.001, y / field.height * (field.rows - 1)));
    const col = Math.floor(gx), row = Math.floor(gy), tx = gx - col, ty = gy - row;
    const a = field.values[row * field.columns + col], b = field.values[row * field.columns + col + 1];
    const c = field.values[(row + 1) * field.columns + col], d = field.values[(row + 1) * field.columns + col + 1];
    return {
        x: (a.x * (1 - tx) + b.x * tx) * (1 - ty) + (c.x * (1 - tx) + d.x * tx) * ty,
        y: (a.y * (1 - tx) + b.y * tx) * (1 - ty) + (c.y * (1 - tx) + d.y * tx) * ty
    };
}
function speed(value) {
    return Math.sqrt(value.x * value.x + value.y * value.y);
}
function bearing(value) {
    return (Math.atan2(-value.x, value.y) * 180 / Math.PI + 360) % 360;
}
function direction(value) {
    if (speed(value) < 0.2)
        return "Calm";
    const degrees = bearing(value);
    return "from " + ["N", "NE", "E", "SE", "S", "SW", "W", "NW"][Math.round(degrees / 45) % 8] + " (" + Math.round(degrees) % 360 + "°)";
}
function seed(index, width, height, radius, cycle) {
    const phase = index * 2.399963229728653 + cycle * 0.79;
    const distance = Math.sqrt(((index * 37 + cycle * 17) % 101 + 0.5) / 101) * radius * 0.96;
    const x = width / 2 + Math.cos(phase) * distance, y = height / 2 + Math.sin(phase) * distance;
    return {
        x: x,
        y: y,
        age: (index % 11) * 0.31,
        life: 6 + index % 5,
        cycle: cycle,
        points: [
            {
                x: x,
                y: y
            }
        ]
    };
}
function inside(x, y, width, height, radius) {
    const dx = x - width / 2, dy = y - height / 2;
    return x >= 0 && y >= 0 && x <= width && y <= height && dx * dx + dy * dy <= radius * radius;
}
// Pixel speed is illustrative and capped; the vector and relative speed come
// only from the forecast. No synthetic turbulence or geographic precision.
function velocity(value) {
    return Math.min(3.6, 48 / Math.max(0.001, speed(value)));
}
// Bound visible length in pixels as well as history size. Lower refresh rates
// must not turn short wind streaks into longer tails. Keep a fractional tail
// segment so trimming stays continuous rather than dropping whole segments.
function trimTrail(points) {
    let length = 0;
    for (let i = points.length - 1; i > 0; --i) {
        const head = points[i], tail = points[i - 1];
        const dx = tail.x - head.x, dy = tail.y - head.y;
        const distance = Math.sqrt(dx * dx + dy * dy);
        if (length + distance > 18) {
            const fraction = (18 - length) / distance;
            tail.x = head.x + dx * fraction;
            tail.y = head.y + dy * fraction;
            points.splice(0, i - 1);
            break;
        }
        length += distance;
    }
    if (points.length > 12)
        points.splice(0, points.length - 12);
}
function advance(particles, field, dt, radius) {
    for (let i = 0; i < particles.length; ++i) {
        const p = particles[i], v = sample(field, p.x, p.y), scale = velocity(v);
        // Midpoint integration follows smoothly curved fields at bounded dt.
        const mid = sample(field, p.x + v.x * scale * dt / 2, p.y + v.y * scale * dt / 2);
        const midScale = velocity(mid);
        p.x += mid.x * midScale * dt;
        p.y += mid.y * midScale * dt;
        p.age += dt;
        if (p.age > p.life || !inside(p.x, p.y, field.width, field.height, radius)) {
            particles[i] = seed(i, field.width, field.height, radius, p.cycle + 1);
            continue;
        }
        p.points.push({
            x: p.x,
            y: p.y
        });
        trimTrail(p.points);
    }
}
function staticTrails(count, field, radius) {
    const result = [];
    for (let i = 0; i < count; ++i) {
        const p = seed(i, field.width, field.height, radius, 0);
        // Build upstream from the head so taper conveys direction without motion.
        for (let step = 0; step < 11; ++step) {
            const v = sample(field, p.x, p.y), scale = velocity(v);
            p.x -= v.x * scale * 0.08;
            p.y -= v.y * scale * 0.08;
            if (!inside(p.x, p.y, field.width, field.height, radius))
                break;
            p.points.unshift({
                x: p.x,
                y: p.y
            });
        }
        trimTrail(p.points);
        result.push(p);
    }
    return result;
}
