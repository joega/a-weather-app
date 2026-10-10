// Geometry checks need no network or display. Run with node from the checkout.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const code = fs.readFileSync(path.join(__dirname, "../../../ui/qml/RadarGeometry.js"), "utf8").replace(/^\.pragma library\s*/, "");
const context = vm.createContext({});
vm.runInContext(code, context);
const halfWorld = 20037508.342789244;
function view(latitude, longitude, zoom) {
    const radius = halfWorld * 2 / Math.pow(2, zoom);
    const centerX = Math.max(-halfWorld + radius, Math.min(halfWorld - radius, longitude / 180 * halfWorld));
    const centerY = Math.max(-halfWorld + radius, Math.min(halfWorld - radius, Math.asinh(Math.tan(latitude * Math.PI / 180)) / Math.PI * halfWorld));
    return [centerX - radius, centerY - radius, centerX + radius, centerY + radius];
}
function checkCoverage(bounds, width, height, ratio) {
    const result = context.layout(...bounds, width, height, ratio);
    assert(result.tiles.length > 0 && result.tiles.length <= 25, "visible cells stay bounded");
    assert(result.zoom >= 0 && result.zoom <= 16);
    const top = (width - Math.min(width, height)) / 2;
    const bottom = width - top;
    // Every displayed point belongs to a fetched cell; corner/edge sampling
    // verifies geographic alignment and cropping independently of tile indices.
    for (const x of [0.001, width / 4, width / 2, width * 3 / 4, width - 0.001])
        for (const y of [top + 0.001, (top + bottom) / 2, bottom - 0.001])
            assert(result.tiles.some(tile => x >= tile.left && x < tile.left + tile.width && y >= tile.top && y < tile.top + tile.height), "tiles cover the visible viewport");
    for (const tile of result.tiles) {
        assert(tile.x >= 0 && tile.x < Math.pow(2, result.zoom));
        assert(tile.y >= 0 && tile.y < Math.pow(2, result.zoom));
        assert(tile.left < width && tile.left + tile.width > 0);
        assert(tile.top < bottom && tile.top + tile.height > top, "no vertically hidden prefetch");
        assert(Math.abs(tile.width - tile.height) < 1e-6, "square mercator cells retain alignment");
    }
    return result;
}
for (const longitude of [-124.9, -98.2, -71.05])
    for (const zoom of [4, 7, 10])
        for (const width of [360, 700, 1200, 1700, 3840])
            for (const height of [340, 650, width])
                for (const ratio of [1, 1.5, 2, 4])
                    checkCoverage(view(42.36, longitude, zoom), width, height, ratio);
const bounds = view(42.36, -71.05, 7);
for (const latitude of [-85, 85])
    for (const longitude of [-180, 180])
        checkCoverage(view(latitude, longitude, 4), 1700, 340, 2);
const wide = checkCoverage(bounds, 1700, 340, 1);
assert(wide.zoom > 7, "wide map uses sharper tiles without changing radar geography");
assert(wide.tiles[0].width <= 425.001, "wide tile magnification is at least halved");
const fullSquare = checkCoverage(bounds, 1700, 1700, 1);
assert(wide.tiles.length < fullSquare.tiles.length, "clipped map requests fewer cells");
assert.equal(context.layout(...bounds, 0, 340, 1).tiles.length, 0);
assert.equal(context.layout(0, 0, 0, 0, 700, 340, 1).tiles.length, 0);
assert.equal(context.layout(NaN, 0, 1, 1, 700, 340, 1).tiles.length, 0);
assert.equal(context.layout(-halfWorld * 2, -halfWorld, halfWorld * 2, halfWorld, 700, 340, 1).tiles.length, 0);
const capped = context.layout(...bounds, 700, 340, 2);
const extreme = context.layout(...bounds, 700, 340, 10);
assert.equal(capped.zoom, extreme.zoom, "DPR beyond two adds no traffic");
assert.equal(capped.tiles.length, extreme.tiles.length);
console.log("Radar geometry: bounded viewport coverage and sharpness checks passed.");
