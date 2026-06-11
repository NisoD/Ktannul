// 3D board renderer for the Catan LAN game. Replaces only the board layer;
// all game logic, modals, and networking stay in index.html. Talks back to
// the page through window.tapVert / tapEdge / tapHex.
import * as THREE from 'three';

const canvas = document.getElementById('board3d');

let renderer, scene, camera, raycaster;
let waterMat, hexTop = 0.25;
let staticGroup = null, boardKey = '';
let tokenY = 0;
let S = null, mode = null;
let robberMesh = null, robberTween = null;
let rafId = 0, lastFrame = 0, visible = false;

const inst = {};       // instanced meshes: roads, setts, cities, hlV, hlE, hlH
const hlMats = [];     // pulsing highlight materials
const texCache = {};

// ---------------------------------------------------------------- setup

const SIDE = { wood: 0x1b4f27, brick: 0x7e3a16, sheep: 0x5f8324, wheat: 0x9c7710, ore: 0x474b54, desert: 0xa98d56 };
const BASE = { wood: '#2e7d3a', brick: '#bd5a2c', sheep: '#9cc454', wheat: '#ddab1c', ore: '#7d8089', desert: '#dcc492' };

renderer = new THREE.WebGLRenderer({ canvas, antialias: true, powerPreference: 'low-power' });
renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
scene = new THREE.Scene();
camera = new THREE.PerspectiveCamera(45, 1, 0.1, 200);
raycaster = new THREE.Raycaster();

scene.add(new THREE.HemisphereLight(0xbfdcff, 0x4a3823, 1.0));
const sun = new THREE.DirectionalLight(0xfff3d6, 1.8);
sun.position.set(5, 9, 4);
scene.add(sun);

waterMat = new THREE.ShaderMaterial({
  uniforms: { t: { value: 0 } },
  vertexShader: `varying vec2 vUv; void main(){ vUv = uv; gl_Position = projectionMatrix*modelViewMatrix*vec4(position,1.0); }`,
  fragmentShader: `
    uniform float t; varying vec2 vUv;
    void main(){
      vec2 p = (vUv - 0.5) * 64.0;
      float d = length(p) / 32.0;
      vec3 deep = vec3(0.022, 0.095, 0.155);
      vec3 mid  = vec3(0.075, 0.27, 0.40);
      vec3 col = mix(mid, deep, smoothstep(0.10, 0.95, d));
      float w1 = sin(p.x*0.8 + t*0.9) * sin(p.y*1.05 - t*0.6);
      float w2 = sin((p.x + p.y)*0.55 - t*0.5);
      col += vec3(0.05, 0.085, 0.10) * smoothstep(0.55, 1.0, w1);
      col += vec3(0.03, 0.05, 0.06)  * smoothstep(0.70, 1.0, w2);
      gl_FragColor = vec4(col, 1.0);
    }`,
});
const water = new THREE.Mesh(new THREE.PlaneGeometry(64, 64).rotateX(-Math.PI / 2), waterMat);
water.position.y = -0.07;
scene.add(water);

// ---------------------------------------------------------------- geometry

function hexShape(r) {
  const s = new THREE.Shape();
  for (let k = 0; k < 6; k++) {
    const a = Math.PI / 180 * (60 * k - 30);
    const x = Math.cos(a) * r, y = Math.sin(a) * r;
    if (k === 0) s.moveTo(x, y); else s.lineTo(x, y);
  }
  s.closePath();
  return s;
}

function flatten(geo) { // shape XY plane -> board XZ plane, base at y=0
  geo.rotateX(Math.PI / 2);
  geo.computeBoundingBox();
  geo.translate(0, -geo.boundingBox.min.y, 0);
  return geo;
}

const hexGeo = flatten(new THREE.ExtrudeGeometry(hexShape(0.985), {
  depth: 0.16, bevelEnabled: true, bevelThickness: 0.05, bevelSize: 0.05, bevelSegments: 2,
}));
hexGeo.computeBoundingBox();
hexTop = hexGeo.boundingBox.max.y;

const shelfGeo = flatten(new THREE.ExtrudeGeometry(hexShape(1.17), { depth: 0.07, bevelEnabled: false }));
const tokenGeo = new THREE.CylinderGeometry(0.30, 0.30, 0.05, 28);
const roadGeo = new THREE.BoxGeometry(0.58, 0.10, 0.13).translate(0, 0.05, 0);

function prismGeo(points, depth) {
  const s = new THREE.Shape();
  points.forEach(([x, y], i) => i === 0 ? s.moveTo(x, y) : s.lineTo(x, y));
  s.closePath();
  const g = new THREE.ExtrudeGeometry(s, { depth, bevelEnabled: false });
  g.translate(0, 0, -depth / 2);
  return g;
}
const settGeo = prismGeo([[-0.16, 0], [0.16, 0], [0.16, 0.15], [0, 0.30], [-0.16, 0.15]], 0.24);
const cityGeo = prismGeo([[-0.20, 0], [0.20, 0], [0.20, 0.15], [0.04, 0.15], [0.04, 0.30], [-0.08, 0.43], [-0.20, 0.30]], 0.26);

const robberGeo = new THREE.LatheGeometry([
  [0.001, 0], [0.15, 0], [0.14, 0.03], [0.08, 0.10], [0.11, 0.155], [0.11, 0.20],
  [0.07, 0.235], [0.095, 0.27], [0.075, 0.33], [0.001, 0.37],
].map(p => new THREE.Vector2(p[0], p[1])), 18);

const hlRingGeo = new THREE.TorusGeometry(0.17, 0.035, 8, 24).rotateX(Math.PI / 2);
const hlEdgeGeo = new THREE.BoxGeometry(0.55, 0.02, 0.16);
const hexRingShape = hexShape(0.92);
hexRingShape.holes.push(new THREE.Path(hexShape(0.78).getPoints()));
const hlHexGeo = new THREE.ShapeGeometry(hexRingShape).rotateX(Math.PI / 2).translate(0, 0.02, 0);

// ---------------------------------------------------------------- textures

function canvasTex(draw, size = 128) {
  const c = document.createElement('canvas');
  c.width = c.height = size;
  draw(c.getContext('2d'), size);
  const tex = new THREE.CanvasTexture(c);
  tex.colorSpace = THREE.SRGBColorSpace;
  return tex;
}

function terrainTex(t) {
  if (texCache[t]) return texCache[t];
  const tex = canvasTex((g, s) => {
    g.fillStyle = BASE[t];
    g.fillRect(0, 0, s, s);
    const rnd = mulberry(t.length * 7919);
    if (t === 'wood') {
      for (let i = 0; i < 16; i++) {
        const x = rnd() * s, y = rnd() * s, r = 6 + rnd() * 8;
        g.fillStyle = `rgba(16,60,28,${0.35 + rnd() * 0.3})`;
        g.beginPath(); g.moveTo(x, y - r); g.lineTo(x + r * .7, y + r); g.lineTo(x - r * .7, y + r); g.fill();
      }
    } else if (t === 'brick') {
      g.strokeStyle = 'rgba(90,35,12,.5)'; g.lineWidth = 3;
      for (let row = 0; row < 6; row++) {
        const y = row * s / 6;
        g.strokeRect(-10 + (row % 2) * 16, y, s / 3, s / 6);
        g.strokeRect(-10 + (row % 2) * 16 + s / 3, y, s / 3, s / 6);
        g.strokeRect(-10 + (row % 2) * 16 + 2 * s / 3, y, s / 3, s / 6);
      }
    } else if (t === 'sheep') {
      for (let i = 0; i < 14; i++) {
        g.fillStyle = `rgba(238,242,220,${0.25 + rnd() * 0.3})`;
        g.beginPath(); g.ellipse(rnd() * s, rnd() * s, 5 + rnd() * 6, 4 + rnd() * 4, 0, 0, 7); g.fill();
      }
    } else if (t === 'wheat') {
      for (let i = 0; i < 22; i++) {
        g.strokeStyle = `rgba(120,85,8,${0.25 + rnd() * 0.3})`; g.lineWidth = 2.5;
        const x = rnd() * s;
        g.beginPath(); g.moveTo(x, rnd() * s); g.quadraticCurveTo(x + 5, 14, x + 2, 28); g.stroke();
      }
    } else if (t === 'ore') {
      for (let i = 0; i < 10; i++) {
        const x = rnd() * s, y = rnd() * s, r = 8 + rnd() * 10;
        g.fillStyle = `rgba(${50 + rnd() * 30 | 0},${52 + rnd() * 30 | 0},${62 + rnd() * 30 | 0},.5)`;
        g.beginPath(); g.moveTo(x, y - r); g.lineTo(x + r, y + r * .6); g.lineTo(x - r * .8, y + r * .8); g.fill();
      }
    } else { // desert
      for (let i = 0; i < 220; i++) {
        g.fillStyle = `rgba(140,110,60,${rnd() * 0.25})`;
        g.fillRect(rnd() * s, rnd() * s, 2.2, 2.2);
      }
    }
  });
  tex.wrapS = tex.wrapT = THREE.RepeatWrapping;
  tex.repeat.set(0.5, 0.5);
  tex.offset.set(0.5, 0.5);
  texCache[t] = tex;
  return tex;
}

function mulberry(a) { // tiny seeded prng so textures are stable per terrain
  return () => {
    a |= 0; a = a + 0x6D2B79F5 | 0;
    let t = Math.imul(a ^ a >>> 15, 1 | a);
    t = t + Math.imul(t ^ t >>> 7, 61 | t) ^ t;
    return ((t ^ t >>> 14) >>> 0) / 4294967296;
  };
}

function tokenTex(n) {
  const key = 'tok' + n;
  if (texCache[key]) return texCache[key];
  const hot = n === 6 || n === 8;
  texCache[key] = canvasTex((g, s) => {
    g.fillStyle = '#f3ead2';
    g.fillRect(0, 0, s, s);
    g.strokeStyle = '#b8a87e'; g.lineWidth = 6;
    g.beginPath(); g.arc(s / 2, s / 2, s / 2 - 8, 0, 7); g.stroke();
    g.fillStyle = hot ? '#c0392b' : '#41382a';
    g.font = `800 ${hot ? 64 : 56}px Georgia, serif`;
    g.textAlign = 'center'; g.textBaseline = 'middle';
    g.fillText(n, s / 2, s / 2 + 4);
  });
  return texCache[key];
}

function labelSprite(text) {
  const tex = canvasTex((g, s) => {
    g.clearRect(0, 0, s, s);
    g.fillStyle = 'rgba(243,234,210,.95)';
    roundRect(g, 8, 38, s - 16, 52, 14); g.fill();
    g.strokeStyle = '#b8a87e'; g.lineWidth = 3; roundRect(g, 8, 38, s - 16, 52, 14); g.stroke();
    g.fillStyle = '#41382a';
    g.font = '800 30px Georgia, serif';
    g.textAlign = 'center'; g.textBaseline = 'middle';
    g.fillText(text, s / 2, 64);
  });
  const sp = new THREE.Sprite(new THREE.SpriteMaterial({ map: tex, depthTest: false }));
  sp.scale.set(1.15, 1.15, 1);
  return sp;
}

function roundRect(g, x, y, w, h, r) {
  g.beginPath();
  g.moveTo(x + r, y);
  g.arcTo(x + w, y, x + w, y + h, r); g.arcTo(x + w, y + h, x, y + h, r);
  g.arcTo(x, y + h, x, y, r); g.arcTo(x, y, x + w, y, r);
  g.closePath();
}

// ---------------------------------------------------------------- static board

function disposeStatic() {
  if (!staticGroup) return;
  staticGroup.traverse(o => {
    if (o.geometry && o.geometry !== hexGeo && o.geometry !== shelfGeo && o.geometry !== tokenGeo) o.geometry.dispose();
    if (o.material && o.material.map && !Object.values(texCache).includes(o.material.map)) o.material.map.dispose();
  });
  scene.remove(staticGroup);
  staticGroup = null;
}

function buildStatic(board) {
  disposeStatic();
  staticGroup = new THREE.Group();

  // sandy shelf under every tile
  const shelf = new THREE.InstancedMesh(shelfGeo, new THREE.MeshLambertMaterial({ color: 0xc8b083 }), board.hexes.length);
  const m = new THREE.Matrix4();
  board.hexes.forEach((h, i) => {
    m.makeTranslation(h.x, -0.055, h.y);
    shelf.setMatrixAt(i, m);
  });
  staticGroup.add(shelf);

  // tiles + tokens
  for (const h of board.hexes) {
    const mesh = new THREE.Mesh(hexGeo, [
      new THREE.MeshLambertMaterial({ map: terrainTex(h.terrain) }),
      new THREE.MeshLambertMaterial({ color: SIDE[h.terrain] }),
    ]);
    mesh.position.set(h.x, 0, h.y);
    staticGroup.add(mesh);
    if (h.number) {
      const tok = new THREE.Mesh(tokenGeo, [
        new THREE.MeshLambertMaterial({ color: 0xe6dcbc }),
        new THREE.MeshLambertMaterial({ map: tokenTex(h.number) }),
        new THREE.MeshLambertMaterial({ color: 0xe6dcbc }),
      ]);
      tok.position.set(h.x, hexTop + 0.025, h.y);
      staticGroup.add(tok);
    }
  }
  tokenY = hexTop + 0.05;

  // ports: boat + label + dashed guides
  const dashPts = [];
  for (const p of board.ports) {
    const v1 = board.verts[p.v1], v2 = board.verts[p.v2];
    const mx = (v1.x + v2.x) / 2, my = (v1.y + v2.y) / 2;
    const len = Math.hypot(mx, my) || 1;
    const ox = mx + mx / len * 0.75, oy = my + my / len * 0.75;
    const boat = new THREE.Group();
    const hull = new THREE.Mesh(new THREE.BoxGeometry(0.42, 0.10, 0.2), new THREE.MeshLambertMaterial({ color: 0x7a4a22 }));
    hull.position.y = 0.05;
    const mast = new THREE.Mesh(new THREE.CylinderGeometry(0.015, 0.015, 0.34, 6), new THREE.MeshLambertMaterial({ color: 0x4d2d12 }));
    mast.position.y = 0.27;
    const sail = new THREE.Mesh(prismGeo([[0, 0], [0.2, 0], [0, 0.26]], 0.01), new THREE.MeshLambertMaterial({ color: 0xf1e6cd, side: THREE.DoubleSide }));
    sail.position.set(0.02, 0.12, 0);
    boat.add(hull, mast, sail);
    boat.position.set(ox, 0, oy);
    boat.lookAt(0, 0, 0);
    staticGroup.add(boat);
    const lbl = labelSprite(p.resource ? `2:1 ${p.resource}` : '3:1 any');
    lbl.position.set(ox, 0.75, oy);
    staticGroup.add(lbl);
    dashPts.push(new THREE.Vector3(v1.x, hexTop, v1.y), new THREE.Vector3(ox, 0.06, oy));
    dashPts.push(new THREE.Vector3(v2.x, hexTop, v2.y), new THREE.Vector3(ox, 0.06, oy));
  }
  const dashGeo = new THREE.BufferGeometry().setFromPoints(dashPts);
  const dashes = new THREE.LineSegments(dashGeo, new THREE.LineDashedMaterial({
    color: 0xcfe3ee, dashSize: 0.09, gapSize: 0.07, transparent: true, opacity: 0.55,
  }));
  dashes.computeLineDistances();
  staticGroup.add(dashes);

  // dynamic instanced pools
  makePools(board);

  // robber
  if (robberMesh) scene.remove(robberMesh);
  robberMesh = new THREE.Mesh(robberGeo, new THREE.MeshLambertMaterial({ color: 0x232031 }));
  robberMesh.position.copy(robberPos(board, board.robber));
  robberTween = null;
  staticGroup.add(robberMesh);

  scene.add(staticGroup);
  fitCamera(board);
}

function makePools(board) {
  for (const k of Object.keys(inst)) { scene.remove(inst[k]); inst[k].dispose(); delete inst[k]; }
  hlMats.length = 0;
  const white = () => new THREE.MeshLambertMaterial({ color: 0xffffff });
  const hl = () => {
    const m = new THREE.MeshBasicMaterial({ color: 0xffe16b, transparent: true, opacity: 0.7, depthWrite: false });
    hlMats.push(m);
    return m;
  };
  inst.roads = new THREE.InstancedMesh(roadGeo, white(), 60);
  inst.setts = new THREE.InstancedMesh(settGeo, white(), 20);
  inst.cities = new THREE.InstancedMesh(cityGeo, white(), 16);
  inst.hlV = new THREE.InstancedMesh(hlRingGeo, hl(), board.verts.length);
  inst.hlE = new THREE.InstancedMesh(hlEdgeGeo, hl(), board.edges.length);
  inst.hlH = new THREE.InstancedMesh(hlHexGeo, hl(), board.hexes.length);
  for (const k of Object.keys(inst)) {
    inst[k].count = 0;
    inst[k].frustumCulled = false;
    scene.add(inst[k]);
  }
}

function robberPos(board, hexId) {
  const h = board.hexes[hexId];
  const off = h.number ? 0.45 : 0;
  return new THREE.Vector3(h.x, hexTop, h.y - off);
}

// ---------------------------------------------------------------- dynamic sync

const M = new THREE.Matrix4();
const COL = new THREE.Color();

function syncDynamic() {
  const b = S.board;
  // roads
  let i = 0;
  for (const [eid, owner] of Object.entries(b.roads || {})) {
    const e = b.edges[eid];
    const v1 = b.verts[e.v1], v2 = b.verts[e.v2];
    M.makeRotationY(Math.atan2(-(v2.y - v1.y), v2.x - v1.x));
    M.setPosition((v1.x + v2.x) / 2, hexTop, (v1.y + v2.y) / 2);
    inst.roads.setMatrixAt(i, M);
    inst.roads.setColorAt(i, COL.set(colorOf(owner)));
    i++;
  }
  setCount(inst.roads, i);

  // buildings
  let si = 0, ci = 0;
  for (const [vid, bd] of Object.entries(b.buildings || {})) {
    const v = b.verts[vid];
    M.makeRotationY(0.4);
    M.setPosition(v.x, hexTop, v.y);
    const pool = bd.type === 'city' ? inst.cities : inst.setts;
    const idx = bd.type === 'city' ? ci++ : si++;
    pool.setMatrixAt(idx, M);
    pool.setColorAt(idx, COL.set(colorOf(bd.player)));
  }
  setCount(inst.setts, si);
  setCount(inst.cities, ci);

  // robber
  if (robberMesh) {
    const dest = robberPos(b, b.robber);
    if (!robberMesh.position.equals(dest)) {
      robberTween = { from: robberMesh.position.clone(), to: dest, t0: performance.now() };
    }
  }

  // highlights
  const place = (pool, ids, fn) => {
    let n = 0;
    for (const id of ids || []) { fn(id); pool.setMatrixAt(n, M); n++; }
    setCount(pool, n);
  };
  place(inst.hlV, mode === 'settlement' ? S.legalSettlements : mode === 'city' ? S.legalCities : [],
    id => { const v = b.verts[id]; M.makeTranslation(v.x, hexTop + 0.03, v.y); });
  place(inst.hlE, mode === 'road' ? S.legalRoads : [],
    id => {
      const e = b.edges[id], v1 = b.verts[e.v1], v2 = b.verts[e.v2];
      M.makeRotationY(Math.atan2(-(v2.y - v1.y), v2.x - v1.x));
      M.setPosition((v1.x + v2.x) / 2, hexTop + 0.03, (v1.y + v2.y) / 2);
    });
  place(inst.hlH, mode === 'robber' ? b.hexes.filter(h => h.id !== b.robber).map(h => h.id) : [],
    id => { const h = b.hexes[id]; M.makeTranslation(h.x, hexTop + 0.01, h.y); });
}

function setCount(pool, n) {
  pool.count = n;
  pool.instanceMatrix.needsUpdate = true;
  if (pool.instanceColor) pool.instanceColor.needsUpdate = true;
}

function colorOf(pid) {
  const p = (S.players || []).find(p => p.id === Number(pid));
  return p ? p.color : '#999';
}

// ---------------------------------------------------------------- camera

const cam = { theta: 0, phi: 0.92, dist: 14, base: 14, target: new THREE.Vector3(), moved: false };

function applyCam() {
  camera.position.set(
    cam.target.x + cam.dist * Math.sin(cam.phi) * Math.sin(cam.theta),
    cam.target.y + cam.dist * Math.cos(cam.phi),
    cam.target.z + cam.dist * Math.sin(cam.phi) * Math.cos(cam.theta));
  camera.lookAt(cam.target);
}

function fitCamera(board) {
  let minX = 1e9, maxX = -1e9, minY = 1e9, maxY = -1e9;
  for (const v of board.verts) {
    minX = Math.min(minX, v.x); maxX = Math.max(maxX, v.x);
    minY = Math.min(minY, v.y); maxY = Math.max(maxY, v.y);
  }
  cam.target.set((minX + maxX) / 2, 0, (minY + maxY) / 2);
  const radius = Math.max(maxX - minX, maxY - minY) / 2 + 1.6;
  const fov = camera.fov * Math.PI / 180;
  const dV = radius / Math.tan(fov / 2);
  const dH = radius / (Math.tan(fov / 2) * Math.max(camera.aspect || 1, 0.3));
  cam.base = Math.max(dV, dH) * 1.04;
  if (!cam.moved) { cam.dist = cam.base; cam.theta = 0; cam.phi = 0.92; }
  cam.dist = Math.min(Math.max(cam.dist, cam.base * 0.45), cam.base * 1.8);
  applyCam();
}

function resize() {
  const w = canvas.parentElement.clientWidth, h = canvas.parentElement.clientHeight;
  if (!w || !h) return;
  renderer.setSize(w, h, false);
  camera.aspect = w / h;
  camera.updateProjectionMatrix();
  if (S && S.board) fitCamera(S.board);
}
new ResizeObserver(resize).observe(canvas.parentElement);

// pointers: 1-finger orbit / tap, 2-finger pinch zoom, double-tap reset
const ptrs = new Map();
let dragging = false, downAt = 0, downXY = null, pinchD = 0, lastTap = 0;

canvas.style.touchAction = 'none';
canvas.addEventListener('pointerdown', e => {
  canvas.setPointerCapture(e.pointerId);
  ptrs.set(e.pointerId, [e.clientX, e.clientY]);
  if (ptrs.size === 1) { dragging = false; downAt = performance.now(); downXY = [e.clientX, e.clientY]; }
  if (ptrs.size === 2) { const a = [...ptrs.values()]; pinchD = Math.hypot(a[0][0] - a[1][0], a[0][1] - a[1][1]); }
});
canvas.addEventListener('pointermove', e => {
  if (!ptrs.has(e.pointerId)) return;
  const prev = ptrs.get(e.pointerId);
  ptrs.set(e.pointerId, [e.clientX, e.clientY]);
  if (ptrs.size === 1) {
    if (!dragging && downXY && Math.hypot(e.clientX - downXY[0], e.clientY - downXY[1]) > 8) dragging = true;
    if (dragging) {
      cam.theta -= (e.clientX - prev[0]) * 0.006;
      cam.phi = Math.min(1.30, Math.max(0.30, cam.phi - (e.clientY - prev[1]) * 0.005));
      cam.moved = true;
      applyCam();
    }
  } else if (ptrs.size === 2) {
    const a = [...ptrs.values()];
    const d = Math.hypot(a[0][0] - a[1][0], a[0][1] - a[1][1]);
    if (pinchD > 0) {
      cam.dist = Math.min(Math.max(cam.dist * pinchD / d, cam.base * 0.45), cam.base * 1.8);
      cam.moved = true;
      applyCam();
    }
    pinchD = d;
  }
});
canvas.addEventListener('pointerup', e => {
  ptrs.delete(e.pointerId);
  if (ptrs.size > 0) return;
  const dt = performance.now() - downAt;
  if (!dragging && dt < 400) {
    const now = performance.now();
    if (now - lastTap < 320) { // double tap → reset view
      cam.moved = false; cam.theta = 0; cam.phi = 0.92; cam.dist = cam.base;
      applyCam();
      lastTap = 0;
    } else {
      lastTap = now;
      pick(e);
    }
  }
});
canvas.addEventListener('pointercancel', e => ptrs.delete(e.pointerId));

// ---------------------------------------------------------------- picking

const pickPlane = new THREE.Plane();
const pickPt = new THREE.Vector3();
const ndc = new THREE.Vector2();

function pick(e) {
  if (!S || !S.board || !mode) return;
  const r = canvas.getBoundingClientRect();
  ndc.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
  raycaster.setFromCamera(ndc, camera);
  pickPlane.set(new THREE.Vector3(0, 1, 0), -hexTop);
  if (!raycaster.ray.intersectPlane(pickPlane, pickPt)) return;
  const bx = pickPt.x, by = pickPt.z;
  const b = S.board;
  const nearest = (ids, posOf, maxD) => {
    let best = -1, bd = maxD;
    for (const id of ids || []) {
      const [x, y] = posOf(id);
      const d = Math.hypot(x - bx, y - by);
      if (d < bd) { bd = d; best = id; }
    }
    return best;
  };
  if (mode === 'settlement' || mode === 'city') {
    const ids = mode === 'settlement' ? S.legalSettlements : S.legalCities;
    const id = nearest(ids, i => [b.verts[i].x, b.verts[i].y], 0.48);
    if (id >= 0) window.tapVert(id);
  } else if (mode === 'road') {
    const id = nearest(S.legalRoads, i => {
      const e2 = b.edges[i];
      return [(b.verts[e2.v1].x + b.verts[e2.v2].x) / 2, (b.verts[e2.v1].y + b.verts[e2.v2].y) / 2];
    }, 0.45);
    if (id >= 0) window.tapEdge(id);
  } else if (mode === 'robber') {
    const ids = b.hexes.filter(h => h.id !== b.robber).map(h => h.id);
    const id = nearest(ids, i => [b.hexes[i].x, b.hexes[i].y], 1.0);
    if (id >= 0) window.tapHex(id);
  }
}

// ---------------------------------------------------------------- loop & API

function frame(t) {
  rafId = requestAnimationFrame(frame);
  if (t - lastFrame < 33) return; // ~30fps is plenty
  lastFrame = t;
  waterMat.uniforms.t.value = t / 1000;
  const pulse = 0.45 + 0.3 * Math.sin(t / 170);
  for (const m of hlMats) m.opacity = pulse;
  if (robberTween && robberMesh) {
    const k = Math.min(1, (performance.now() - robberTween.t0) / 350);
    robberMesh.position.lerpVectors(robberTween.from, robberTween.to, k * k * (3 - 2 * k));
    if (k >= 1) robberTween = null;
  }
  renderer.render(scene, camera);
}

export function setVisible(on) {
  if (on === visible) return;
  visible = on;
  if (on) { resize(); if (!rafId) rafId = requestAnimationFrame(frame); }
  else if (rafId) { cancelAnimationFrame(rafId); rafId = 0; }
}

export function update(state, m) {
  S = state;
  mode = m;
  if (!S.board) return;
  const key = S.board.hexes.map(h => h.terrain + h.number).join(',');
  if (key !== boardKey) { boardKey = key; buildStatic(S.board); }
  syncDynamic();
}

// screen position of a board point (for fx overlays and tests)
export function boardToScreen(x, y) {
  const v = new THREE.Vector3(x, hexTop, y).project(camera);
  const r = canvas.getBoundingClientRect();
  return [r.left + (v.x * 0.5 + 0.5) * r.width, r.top + (-v.y * 0.5 + 0.5) * r.height];
}

export function hexToScreen(hexId) {
  if (!S || !S.board) return null;
  const h = S.board.hexes[hexId];
  return boardToScreen(h.x, h.y);
}

export function vertToScreen(vid) {
  if (!S || !S.board) return null;
  const v = S.board.verts[vid];
  return boardToScreen(v.x, v.y);
}

export function edgeToScreen(eid) {
  if (!S || !S.board) return null;
  const e = S.board.edges[eid];
  const v1 = S.board.verts[e.v1], v2 = S.board.verts[e.v2];
  return boardToScreen((v1.x + v2.x) / 2, (v1.y + v2.y) / 2);
}

document.addEventListener('visibilitychange', () => {
  if (document.hidden) { if (rafId) { cancelAnimationFrame(rafId); rafId = 0; } }
  else if (visible && !rafId) rafId = requestAnimationFrame(frame);
});
