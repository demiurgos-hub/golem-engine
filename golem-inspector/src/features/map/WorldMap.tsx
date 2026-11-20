import { useCallback, useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent, type WheelEvent as ReactWheelEvent } from 'react';
import { useEntityStore, useInspector } from '../../hooks/useInspector';
import { typeColor, typeShape, type EntityShape, type Plane } from '../../lib/entityTypes';
import { effectiveMapSpace, type MapSpace } from '../../lib/filters';
import type { EntityRecord } from '../../store/entityStore';

/** Camera over the map plane: a is world X, b is world Z or Y (screen down). */
interface Camera {
  ca: number;
  cb: number;
  /** Pixels per world unit */
  scale: number;
}

const MIN_SCALE = 0.05;
const MAX_SCALE = 200;
const DEFAULT_CAMERA: Camera = { ca: 0, cb: 0, scale: 8 };
const RULER_TOP = 22;
const RULER_LEFT = 44;
const CLICK_MOVE_PX = 6;
const PICK_RADIUS_PX = 14;

function axisB(e: EntityRecord, plane: Plane): number {
  return plane === 'xz' ? e.position.z : e.position.y;
}

function worldToScreen(a: number, b: number, cam: Camera, w: number, h: number) {
  return { sx: w / 2 + (a - cam.ca) * cam.scale, sy: h / 2 + (b - cam.cb) * cam.scale };
}

function screenToWorld(sx: number, sy: number, cam: Camera, w: number, h: number) {
  return { a: cam.ca + (sx - w / 2) / cam.scale, b: cam.cb + (sy - h / 2) / cam.scale };
}

function drawShape(ctx: CanvasRenderingContext2D, shape: EntityShape, sx: number, sy: number, size: number, fill: string, selected: boolean) {
  ctx.save();
  ctx.translate(sx, sy);
  ctx.fillStyle = fill;
  ctx.strokeStyle = selected ? '#f5f0e6' : 'rgba(0,0,0,0.45)';
  ctx.lineWidth = selected ? 2.5 : 1;
  ctx.beginPath();
  switch (shape) {
    case 'circle':
      ctx.arc(0, 0, size, 0, Math.PI * 2);
      break;
    case 'square':
      ctx.rect(-size, -size, size * 2, size * 2);
      break;
    case 'diamond':
      ctx.moveTo(0, -size);
      ctx.lineTo(size, 0);
      ctx.lineTo(0, size);
      ctx.lineTo(-size, 0);
      ctx.closePath();
      break;
    case 'triangle':
      ctx.moveTo(0, -size);
      ctx.lineTo(size, size);
      ctx.lineTo(-size, size);
      ctx.closePath();
      break;
    case 'cross':
      ctx.moveTo(-size, 0);
      ctx.lineTo(size, 0);
      ctx.moveTo(0, -size);
      ctx.lineTo(0, size);
      ctx.lineWidth = selected ? 3 : 2;
      ctx.strokeStyle = fill;
      ctx.stroke();
      if (selected) {
        ctx.beginPath();
        ctx.arc(0, 0, size + 3, 0, Math.PI * 2);
        ctx.strokeStyle = '#f5f0e6';
        ctx.lineWidth = 1.5;
        ctx.stroke();
      }
      ctx.restore();
      return;
  }
  ctx.fill();
  ctx.stroke();
  ctx.restore();
}

function fitCamera(entities: EntityRecord[], plane: Plane, width: number, height: number): Camera {
  if (entities.length === 0) return { ...DEFAULT_CAMERA };
  let minA = Infinity;
  let maxA = -Infinity;
  let minB = Infinity;
  let maxB = -Infinity;
  for (const e of entities) {
    const b = axisB(e, plane);
    minA = Math.min(minA, e.position.x);
    maxA = Math.max(maxA, e.position.x);
    minB = Math.min(minB, b);
    maxB = Math.max(maxB, b);
  }
  const pad = 8;
  const spanA = Math.max(maxA - minA, 10) + pad * 2;
  const spanB = Math.max(maxB - minB, 10) + pad * 2;
  const scale = Math.min(MAX_SCALE, Math.max(MIN_SCALE, Math.min(width / spanA, height / spanB) * 0.9));
  return { ca: (minA + maxA) / 2, cb: (minB + maxB) / 2, scale };
}

function pickEntity(entities: EntityRecord[], plane: Plane, a: number, b: number, cam: Camera): EntityRecord | null {
  let best: EntityRecord | null = null;
  let bestDist = PICK_RADIUS_PX / cam.scale;
  for (const e of entities) {
    const d = Math.hypot(e.position.x - a, axisB(e, plane) - b);
    if (d < bestDist) {
      bestDist = d;
      best = e;
    }
  }
  return best;
}

export function WorldMap() {
  const { filteredEntities: listEntities, selectedId, selectEntity, plane, setPlane, selectedTargetId, spaces } = useInspector();
  const { schema } = useEntityStore();
  // Spaces share coordinates, so the map shows one space at a time by default.
  const [spaceChoice, setSpaceChoice] = useState<{ target: string | null; space: MapSpace } | null>(null);
  const chosenSpace = spaceChoice?.target === selectedTargetId ? spaceChoice.space : null;
  const mapSpace = effectiveMapSpace(chosenSpace, spaces);
  const filteredEntities = useMemo(
    () => (mapSpace === 'all' ? listEntities : listEntities.filter((e) => e.space === mapSpace)),
    [listEntities, mapSpace],
  );
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const viewportRef = useRef<HTMLDivElement>(null);
  const [camera, setCamera] = useState<Camera>(DEFAULT_CAMERA);
  const cameraRef = useRef(camera);
  cameraRef.current = camera;
  const entitiesRef = useRef(filteredEntities);
  entitiesRef.current = filteredEntities;
  const selectedRef = useRef(selectedId);
  selectedRef.current = selectedId;
  const dragRef = useRef<{ pointerId: number; lastX: number; lastY: number; moved: boolean } | null>(null);
  const [size, setSize] = useState({ w: 1, h: 1 });
  const sizeRef = useRef(size);
  sizeRef.current = size;
  const [cursor, setCursor] = useState<{ a: number; b: number } | null>(null);
  const bName = plane === 'xz' ? 'Z' : 'Y';

  useEffect(() => {
    const el = viewportRef.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (!entry) return;
      setSize({ w: Math.max(1, Math.floor(entry.contentRect.width)), h: Math.max(1, Math.floor(entry.contentRect.height)) });
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const draw = useCallback(() => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext('2d');
    if (!canvas || !ctx) return;
    const dpr = window.devicePixelRatio || 1;
    const { w, h } = size;
    canvas.width = Math.floor(w * dpr);
    canvas.height = Math.floor(h * dpr);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    const cam = cameraRef.current;

    ctx.fillStyle = '#0d1219';
    ctx.fillRect(0, 0, w, h);

    const step = niceGridStep(cam.scale);
    const tl = screenToWorld(0, 0, cam, w, h);
    const br = screenToWorld(w, h, cam, w, h);
    const a0 = Math.floor(tl.a / step) * step;
    const b0 = Math.floor(tl.b / step) * step;
    ctx.strokeStyle = 'rgba(180, 190, 210, 0.08)';
    ctx.lineWidth = 1;
    ctx.beginPath();
    for (let a = a0; a <= br.a + step; a += step) {
      const { sx } = worldToScreen(a, 0, cam, w, h);
      ctx.moveTo(sx, 0);
      ctx.lineTo(sx, h);
    }
    for (let b = b0; b <= br.b + step; b += step) {
      const { sy } = worldToScreen(0, b, cam, w, h);
      ctx.moveTo(0, sy);
      ctx.lineTo(w, sy);
    }
    ctx.stroke();

    const origin = worldToScreen(0, 0, cam, w, h);
    ctx.strokeStyle = 'rgba(120, 160, 200, 0.35)';
    ctx.beginPath();
    ctx.moveTo(0, origin.sy);
    ctx.lineTo(w, origin.sy);
    ctx.moveTo(origin.sx, 0);
    ctx.lineTo(origin.sx, h);
    ctx.stroke();

    const entities = entitiesRef.current;
    const selected = selectedRef.current;
    const sizePx = Math.max(3, Math.min(10, cam.scale * 0.55));
    let selectedRecord: EntityRecord | null = null;
    for (const e of entities) {
      const { sx, sy } = worldToScreen(e.position.x, axisB(e, plane), cam, w, h);
      if (e.id === selected) selectedRecord = e;
      if (sx < -20 || sy < -20 || sx > w + 20 || sy > h + 20) continue;
      drawShape(ctx, typeShape(schema, e.typeName), sx, sy, sizePx, typeColor(e.typeName), e.id === selected);
    }
    if (selectedRecord) {
      const { sx, sy } = worldToScreen(selectedRecord.position.x, axisB(selectedRecord, plane), cam, w, h);
      ctx.fillStyle = 'rgba(245, 240, 230, 0.9)';
      ctx.font = '11px ui-monospace, monospace';
      ctx.fillText(`${selectedRecord.typeName} #${selectedRecord.id}`, sx + sizePx + 6, sy - 4);
    }
    drawRulers(ctx, cam, w, h, step, a0, b0, br.a, br.b);
  }, [size, plane, schema]);

  useEffect(() => {
    draw();
  }, [draw, filteredEntities, selectedId, camera]);

  const clientToCanvas = (clientX: number, clientY: number) => {
    const canvas = canvasRef.current;
    if (!canvas) return { sx: 0, sy: 0 };
    const rect = canvas.getBoundingClientRect();
    const { w, h } = sizeRef.current;
    if (rect.width <= 0 || rect.height <= 0) return { sx: 0, sy: 0 };
    return { sx: ((clientX - rect.left) / rect.width) * w, sy: ((clientY - rect.top) / rect.height) * h };
  };

  // React attaches wheel listeners as passive, so preventDefault needs a
  // native non-passive listener.
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const block = (e: WheelEvent) => e.preventDefault();
    canvas.addEventListener('wheel', block, { passive: false });
    return () => canvas.removeEventListener('wheel', block);
  }, []);

  const onWheel = (e: ReactWheelEvent) => {
    const { sx, sy } = clientToCanvas(e.clientX, e.clientY);
    const cam = cameraRef.current;
    const { w, h } = sizeRef.current;
    const before = screenToWorld(sx, sy, cam, w, h);
    const next = Math.min(MAX_SCALE, Math.max(MIN_SCALE, cam.scale * (e.deltaY > 0 ? 0.9 : 1.1)));
    setCamera({ scale: next, ca: before.a - (sx - w / 2) / next, cb: before.b - (sy - h / 2) / next });
    setCursor(before);
  };

  const onPointerDown = (e: ReactPointerEvent) => {
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
    dragRef.current = { pointerId: e.pointerId, lastX: e.clientX, lastY: e.clientY, moved: false };
  };

  const onPointerMove = (e: ReactPointerEvent) => {
    const drag = dragRef.current;
    let cam = cameraRef.current;
    if (drag && drag.pointerId === e.pointerId) {
      const dx = e.clientX - drag.lastX;
      const dy = e.clientY - drag.lastY;
      if (Math.hypot(dx, dy) > CLICK_MOVE_PX) drag.moved = true;
      drag.lastX = e.clientX;
      drag.lastY = e.clientY;
      cam = { ...cam, ca: cam.ca - dx / cam.scale, cb: cam.cb - dy / cam.scale };
      cameraRef.current = cam;
      setCamera(cam);
    }
    const { sx, sy } = clientToCanvas(e.clientX, e.clientY);
    setCursor(screenToWorld(sx, sy, cam, sizeRef.current.w, sizeRef.current.h));
  };

  const onPointerUp = (e: ReactPointerEvent) => {
    const drag = dragRef.current;
    dragRef.current = null;
    if (!drag || drag.moved) return;
    const { sx, sy } = clientToCanvas(e.clientX, e.clientY);
    const world = screenToWorld(sx, sy, cameraRef.current, sizeRef.current.w, sizeRef.current.h);
    selectEntity(pickEntity(entitiesRef.current, plane, world.a, world.b, cameraRef.current)?.id ?? null);
  };

  const fit = () => setCamera(fitCamera(entitiesRef.current, plane, sizeRef.current.w, sizeRef.current.h));

  // Fit once per target, plane and chosen map space when entities first
  // appear; a second space appearing does not move the camera.
  const fittedFor = useRef<string | null>(null);
  useEffect(() => {
    const key = `${selectedTargetId}:${plane}:${chosenSpace ?? 'default'}`;
    if (fittedFor.current !== key && filteredEntities.length > 0 && size.w > 1) {
      fittedFor.current = key;
      setCamera(fitCamera(filteredEntities, plane, size.w, size.h));
    }
  }, [filteredEntities, size, plane, selectedTargetId, chosenSpace]);

  const hud = cursor == null ? '—' : `X ${formatHud(cursor.a, camera.scale)}  ·  ${bName} ${formatHud(cursor.b, camera.scale)}`;

  return (
    <div className="map-panel">
      <div className="map-toolbar">
        <span className="map-title">Top-down · X/{bName}</span>
        <span className="map-cursor-hud" aria-live="polite">
          {hud}
        </span>
        {spaces.length > 1 ? (
          <label className="map-space">
            <span>Space</span>
            <select
              value={String(mapSpace)}
              onChange={(e) => setSpaceChoice({ target: selectedTargetId, space: e.target.value === 'all' ? 'all' : Number(e.target.value) })}
            >
              {spaces.map((sp) => (
                <option key={sp} value={sp}>
                  {sp}
                </option>
              ))}
              <option value="all">All</option>
            </select>
          </label>
        ) : null}
        <div className="plane-toggle" role="group" aria-label="Map plane">
          {(['xz', 'xy'] as const).map((p) => (
            <button key={p} type="button" className={`btn btn--small${plane === p ? ' btn--on' : ''}`} aria-pressed={plane === p} onClick={() => setPlane(p)}>
              {p.toUpperCase()}
            </button>
          ))}
        </div>
        <button type="button" className="btn" onClick={fit}>
          Fit
        </button>
      </div>
      <div className="map-viewport" ref={viewportRef}>
        <canvas
          ref={canvasRef}
          className="world-canvas"
          aria-label="Entity map"
          onWheel={onWheel}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerLeave={() => {
            if (!dragRef.current) setCursor(null);
          }}
          onPointerCancel={() => {
            dragRef.current = null;
          }}
        />
      </div>
    </div>
  );
}

function niceGridStep(scale: number): number {
  const raw = 48 / scale;
  const pow = Math.pow(10, Math.floor(Math.log10(raw)));
  const n = raw / pow;
  return (n < 2 ? 1 : n < 5 ? 2 : 5) * pow;
}

function formatCoord(value: number, step: number): string {
  const decimals = step >= 1 ? 0 : step >= 0.1 ? 1 : step >= 0.01 ? 2 : 3;
  return (Math.round(value / step) * step).toFixed(decimals);
}

function formatHud(value: number, scale: number): string {
  return value.toFixed(scale >= 20 ? 2 : scale >= 5 ? 1 : 0);
}

function drawRulers(ctx: CanvasRenderingContext2D, cam: Camera, w: number, h: number, step: number, a0: number, b0: number, aMax: number, bMax: number) {
  ctx.fillStyle = 'rgba(13, 18, 25, 0.92)';
  ctx.fillRect(0, 0, w, RULER_TOP);
  ctx.fillRect(0, 0, RULER_LEFT, h);
  ctx.strokeStyle = 'rgba(180, 190, 210, 0.18)';
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(0, RULER_TOP + 0.5);
  ctx.lineTo(w, RULER_TOP + 0.5);
  ctx.moveTo(RULER_LEFT + 0.5, 0);
  ctx.lineTo(RULER_LEFT + 0.5, h);
  ctx.stroke();

  ctx.font = '10px ui-monospace, monospace';
  ctx.fillStyle = 'rgba(200, 210, 225, 0.85)';
  ctx.strokeStyle = 'rgba(180, 190, 210, 0.45)';
  const stride = Math.max(1, Math.ceil(56 / (step * cam.scale)));

  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  for (let a = a0; a <= aMax + step; a += step) {
    const { sx } = worldToScreen(a, 0, cam, w, h);
    if (sx < RULER_LEFT + 8 || sx > w - 4) continue;
    ctx.beginPath();
    ctx.moveTo(sx + 0.5, RULER_TOP - 5);
    ctx.lineTo(sx + 0.5, RULER_TOP);
    ctx.stroke();
    if (Math.round(a / step) % stride === 0) ctx.fillText(formatCoord(a, step), sx, RULER_TOP / 2);
  }
  ctx.textAlign = 'right';
  for (let b = b0; b <= bMax + step; b += step) {
    const { sy } = worldToScreen(0, b, cam, w, h);
    if (sy < RULER_TOP + 8 || sy > h - 4) continue;
    ctx.beginPath();
    ctx.moveTo(RULER_LEFT - 5, sy + 0.5);
    ctx.lineTo(RULER_LEFT, sy + 0.5);
    ctx.stroke();
    if (Math.round(b / step) % stride === 0) ctx.fillText(formatCoord(b, step), RULER_LEFT - 8, sy);
  }
}
