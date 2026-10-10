.pragma library

const extent = 20037508.342789244;
const world = extent * 2;
const maximumTiles = 25;

// The radar image retains its original geographic square. Only the background
// uses a higher XYZ zoom; tiles outside the vertically clipped viewport aren't
// requested. Native downloads retain their independent 16-request admission cap.
function layout(west, south, east, north, width, viewportHeight, devicePixelRatio) {
    if (![west, south, east, north, width, viewportHeight].every(Number.isFinite) || east <= west || north <= south || width < 1 || viewportHeight < 1)
        return {
            zoom: 0,
            tiles: []
        };
    const span = east - west, verticalSpan = north - south;
    if (span > world || verticalSpan > world)
        return {
            zoom: 0,
            tiles: []
        };
    const ratio = Number.isFinite(devicePixelRatio) ? Math.max(1, Math.min(2, devicePixelRatio)) : 1;
    const physicalWidth = Math.min(2048, width * ratio);
    const baseZoom = Math.max(0, Math.min(16, Math.round(Math.log(world * 2 / span) / Math.LN2)));
    let zoom = Math.max(baseZoom, Math.min(16, Math.ceil(Math.log(world * physicalWidth / (span * 256)) / Math.LN2)));
    const visibleFraction = Math.min(1, viewportHeight / width);
    const visibleNorth = north - verticalSpan * (1 - visibleFraction) / 2;
    const visibleSouth = south + verticalSpan * (1 - visibleFraction) / 2;
    function range(level) {
        const n = Math.pow(2, level), unit = world / n;
        return {
            n: n,
            unit: unit,
            x0: Math.floor((west + extent) / unit),
            x1: Math.ceil((east + extent) / unit - 1e-9) - 1,
            y0: Math.max(0, Math.floor((extent - visibleNorth) / unit)),
            y1: Math.min(n - 1, Math.ceil((extent - visibleSouth) / unit - 1e-9) - 1)
        };
    }
    let cells = range(zoom);
    while (zoom > 0 && (cells.x1 - cells.x0 + 1) * Math.max(0, cells.y1 - cells.y0 + 1) > maximumTiles)
        cells = range(--zoom);
    const tiles = [];
    for (let y = cells.y0; y <= cells.y1; ++y)
        for (let x = cells.x0; x <= cells.x1; ++x) {
            const wrappedX = (x % cells.n + cells.n) % cells.n;
            tiles.push({
                key: zoom + "/" + wrappedX + "/" + y,
                zoom: zoom,
                x: wrappedX,
                y: y,
                left: (x * cells.unit - extent - west) / span * width,
                top: (north - extent + y * cells.unit) / verticalSpan * width,
                width: cells.unit / span * width,
                height: cells.unit / verticalSpan * width
            });
        }
    return {
        zoom: zoom,
        tiles: tiles
    };
}
