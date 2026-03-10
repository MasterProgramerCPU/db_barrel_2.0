/* ============================================================
   DB Barrel 2.0 — Windows XP Light Mode (v6)
   Search, reload, status indicators, replication, indexes
   ============================================================ */

(function () {
    'use strict';

    const galaxyScreen = document.getElementById('galaxy-screen');
    const schemaScreen = document.getElementById('schema-screen');
    const logoHome = document.getElementById('logo-home');
    const currentDbLabel = document.getElementById('current-db-name');
    const dbCountBadge = document.getElementById('db-count-badge');
    const detailOverlay = document.getElementById('detail-overlay');
    const detailTableName = document.getElementById('detail-table-name');
    const detailContent = document.getElementById('detail-content');
    const closeDetailBtn = document.getElementById('close-detail');
    const reloadBtn = document.getElementById('reload-btn');
    const schemaSearch = document.getElementById('schema-search');
    const searchCount = document.getElementById('search-count');
    const searchClear = document.getElementById('search-clear');
    const toastEl = document.getElementById('toast');
    const reloadLabelDefault = 'Reload';
    const reloadLabelBusy = 'Reloading...';

    let databases = [];
    let replication = [];
    let currentSchema = null;
    let currentDbId = null;
    let galaxySim = null;

    function normalizeName(v) {
        return String(v || '').trim().toLowerCase();
    }

    // XP DB type colors
    const DB_CLR = {
        postgresql: { fill: '#B8D4F0', stroke: '#336791', text: '#003366', hdr: 'linear-gradient(180deg, #4A8CC7 0%, #336791 100%)' },
        mysql: { fill: '#FFF0D0', stroke: '#F29111', text: '#8B4500', hdr: 'linear-gradient(180deg, #F5A623 0%, #F29111 100%)' },
        mariadb: { fill: '#F0E0D4', stroke: '#C0765A', text: '#5E3322', hdr: 'linear-gradient(180deg, #D4896A 0%, #C0765A 100%)' },
        sqlite: { fill: '#D0E8F8', stroke: '#4F9CD0', text: '#1A4970', hdr: 'linear-gradient(180deg, #6AB0DC 0%, #4F9CD0 100%)' },
    };
    const DEF_CLR = { fill: '#E0E0E0', stroke: '#888', text: '#333', hdr: '#888' };
    function clr(d) { return DB_CLR[d] || DEF_CLR; }

    // ---- Toast ----
    let toastTimer = null;
    function showToast(msg) {
        toastEl.textContent = msg;
        toastEl.hidden = false;
        clearTimeout(toastTimer);
        toastTimer = setTimeout(() => { toastEl.hidden = true; }, 2500);
    }

    // ---- Navigation ----
    function showGallery() {
        schemaScreen.hidden = true;
        galaxyScreen.hidden = false;
        galaxyScreen.classList.add('fade-in');
        currentDbLabel.hidden = true;
        currentSchema = null;
        currentDbId = null;
        detailOverlay.hidden = true;
        schemaSearch.value = '';
        searchCount.hidden = true;
        searchClear.hidden = true;
        if (galaxySim) galaxySim.alpha(0.3).restart();
    }

    function showSchema(name) {
        galaxyScreen.hidden = true;
        if (galaxySim) galaxySim.stop();
        schemaScreen.hidden = false;
        schemaScreen.classList.add('fade-in');
        currentDbLabel.textContent = name;
        currentDbLabel.hidden = false;
    }

    logoHome.addEventListener('click', showGallery);

    // ---- Reload ----
    reloadBtn.addEventListener('click', async () => {
        reloadBtn.classList.add('is-loading');
        reloadBtn.disabled = true;
        reloadBtn.textContent = reloadLabelBusy;
        try {
            const r = await fetch('/api/reload', { method: 'POST' });
            const d = await r.json();
            if (!r.ok) throw new Error(d.error || 'Reload failed');
            showToast(`✅ Reloaded ${d.databases} database(s)`);
            // Re-fetch everything
            await boot();
            // If we were viewing a schema, re-open it
            if (currentDbId !== null) {
                const db = databases.find(x => x.id === currentDbId);
                if (db && db.status === 'ok') openDb(db);
                else showGallery();
            }
        } catch (e) {
            showToast('❌ Reload failed: ' + e.message);
            console.error(e);
        } finally {
            reloadBtn.classList.remove('is-loading');
            reloadBtn.disabled = false;
            reloadBtn.textContent = reloadLabelDefault;
        }
    });

    // ---- Load ----
    async function boot() {
        try {
            const [dbRes, topoRes] = await Promise.all([
                fetch('/api/databases'),
                fetch('/api/topology'),
            ]);
            const dbData = await dbRes.json();
            const topoData = await topoRes.json();
            databases = Array.isArray(dbData) ? dbData : [];
            replication = Array.isArray(topoData) ? topoData : [];
            dbCountBadge.textContent = databases.length + ' database' + (databases.length !== 1 ? 's' : '');
            renderGalaxy(databases);
        } catch (e) { console.error(e); }
    }

    // ---- Galaxy: XP-style nodes, zoomable ----
    function renderGalaxy(dbs) {
        d3.select('#galaxy-svg').selectAll('*').remove();
        const el = document.getElementById('galaxy-svg');
        const W = el.clientWidth || window.innerWidth;
        const H = el.clientHeight || (window.innerHeight - 54);

        const svg = d3.select('#galaxy-svg').attr('viewBox', [0, 0, W, H]);

        // Drop shadow filter
        const defs = svg.append('defs');
        const shf = defs.append('filter').attr('id', 'xp-shadow')
            .attr('x', '-10%').attr('y', '-10%').attr('width', '130%').attr('height', '140%');
        shf.append('feDropShadow').attr('dx', 2).attr('dy', 2).attr('stdDeviation', 3).attr('flood-opacity', 0.2);

        // Replication arrow marker
        defs.append('marker').attr('id', 'repl-arr').attr('viewBox', '0 -5 10 10')
            .attr('refX', 8).attr('refY', 0).attr('markerWidth', 8).attr('markerHeight', 8).attr('orient', 'auto')
            .append('path').attr('d', 'M0,-5L10,0L0,5').attr('fill', '#9333EA').attr('fill-opacity', 0.6);

        const root = svg.append('g');

        // ZOOM on galaxy
        const zoom = d3.zoom()
            .scaleExtent([0.3, 3])
            .on('zoom', ev => root.attr('transform', ev.transform));
        svg.call(zoom);

        const NODE_W = 142, NODE_H = 158, ICON_SIZE = 90;
        const nodes = dbs.map((db, i) => ({
            ...db, index: i,
            x: W / 2 + (Math.random() - 0.5) * 300,
            y: H / 2 + (Math.random() - 0.5) * 200,
        }));

        // Build exact and normalized lookup maps for replication endpoints.
        const nameMapExact = {};
        const nameMapNormalized = {};
        nodes.forEach(n => {
            const exact = String(n.name || '').trim();
            if (exact && !nameMapExact[exact]) nameMapExact[exact] = n;

            const normalized = normalizeName(exact);
            if (!normalized) return;
            if (!(normalized in nameMapNormalized)) {
                nameMapNormalized[normalized] = n;
            } else if (nameMapNormalized[normalized] !== n) {
                // Ambiguous case-insensitive match; force exact-name matching.
                nameMapNormalized[normalized] = null;
            }
        });

        function resolveReplicationNode(name) {
            const exact = String(name || '').trim();
            if (exact && nameMapExact[exact]) return nameMapExact[exact];
            return nameMapNormalized[normalizeName(exact)] || null;
        }

        // Replication links data
        const replLinks = replication.map(r => ({
            source: resolveReplicationNode(r.sourceName),
            target: resolveReplicationNode(r.targetName),
            type: r.type,
            details: r.details || ''
        })).filter(r => r.source && r.target && r.source !== r.target);

        // Orbit lines (between all nodes)
        const orbits = root.append('g');
        for (let i = 0; i < nodes.length; i++)
            for (let j = i + 1; j < nodes.length; j++)
                orbits.append('line').attr('class', 'orbit-line').attr('data-i', i).attr('data-j', j);

        // Replication arrow group
        const replG = root.append('g');
        const replPaths = replG.selectAll('.repl-link').data(replLinks).enter()
            .append('path').attr('class', 'repl-link').attr('marker-end', 'url(#repl-arr)');
        const replLabels = replG.selectAll('.repl-label').data(replLinks).enter()
            .append('text').attr('class', 'repl-label').text(replicationLabel);
        replLabels.append('title').text(d => {
            const kind = d.type || 'replica';
            return d.details ? `${kind}: ${d.details}` : kind;
        });

        const grp = root.append('g');
        const nodeEls = grp.selectAll('.db-node').data(nodes).enter().append('g')
            .attr('class', d => 'db-node' + (d.status === 'error' ? ' error-node' : ''))
            .attr('transform', d => `translate(${d.x},${d.y})`);

        // Card background
        nodeEls.append('rect')
            .attr('class', 'node-border')
            .attr('x', -NODE_W / 2).attr('y', -28)
            .attr('width', NODE_W).attr('height', NODE_H)
            .attr('rx', 4)
            .attr('fill', d => clr(d.driver).fill)
            .attr('stroke', d => clr(d.driver).stroke)
            .attr('stroke-width', 2)
            .attr('filter', 'url(#xp-shadow)');

        // Card titlebar
        nodeEls.append('rect')
            .attr('x', -NODE_W / 2 + 1).attr('y', -27)
            .attr('width', NODE_W - 2).attr('height', 16)
            .attr('rx', 3)
            .attr('fill', d => clr(d.driver).stroke)
            .attr('opacity', 0.15);

        // Status indicator (animated dot)
        nodeEls.append('circle')
            .attr('class', d => 'status-dot ' + (d.status === 'ok' ? 'ok' : 'error'))
            .attr('cx', NODE_W / 2 - 14).attr('cy', -16)
            .attr('r', 5);

        // DB logo
        nodeEls.append('image')
            .attr('href', d => `svg/${d.driver}.svg`)
            .attr('x', -ICON_SIZE / 2).attr('y', -12)
            .attr('width', ICON_SIZE).attr('height', ICON_SIZE)
            .style('pointer-events', 'none');

        // Name
        nodeEls.append('text').attr('class', 'node-label')
            .attr('y', 92).text(d => d.name);

        // Driver badge
        nodeEls.append('text').attr('class', 'node-driver')
            .attr('y', 106)
            .attr('fill', d => clr(d.driver).text)
            .text(d => d.driver);

        // Host info label
        nodeEls.append('text').attr('class', 'node-host')
            .attr('y', 118)
            .text(d => {
                if (d.driver === 'sqlite') return d.host || 'local file';
                if (d.host && d.port) return d.host + ':' + d.port;
                if (d.host) return d.host;
                return '';
            });

        // Error label
        nodeEls.filter(d => d.status === 'error').each(function (d) {
            d3.select(this).append('text').attr('class', 'node-error').attr('y', NODE_H - 24).text('Connection Failed');
        });

        // Click
        nodeEls.filter(d => d.status === 'ok')
            .on('click', (ev, d) => { ev.stopPropagation(); openDb(d); })
            .style('cursor', 'pointer');

        // Force
        galaxySim = d3.forceSimulation(nodes)
            .force('center', d3.forceCenter(W / 2, H / 2))
            .force('charge', d3.forceManyBody().strength(-500))
            .force('collision', d3.forceCollide().radius(95))
            .force('x', d3.forceX(W / 2).strength(0.04))
            .force('y', d3.forceY(H / 2).strength(0.04))
            .alphaDecay(0.015)
            .on('tick', () => {
                nodeEls.attr('transform', d => `translate(${d.x},${d.y})`);
                orbits.selectAll('.orbit-line')
                    .attr('x1', function () { return nodes[+this.dataset.i].x; })
                    .attr('y1', function () { return nodes[+this.dataset.i].y; })
                    .attr('x2', function () { return nodes[+this.dataset.j].x; })
                    .attr('y2', function () { return nodes[+this.dataset.j].y; });
                // Update replication arrows
                replPaths.attr('d', d => {
                    const sx = d.source.x, sy = d.source.y;
                    const tx = d.target.x, ty = d.target.y;
                    const mx = (sx + tx) / 2, my = (sy + ty) / 2;
                    const dx = tx - sx, dy = ty - sy;
                    const dist = Math.sqrt(dx * dx + dy * dy) || 1;
                    const off = dist * 0.15;
                    const nx = -dy / dist, ny = dx / dist;
                    return `M${sx},${sy} Q${mx + nx * off},${my + ny * off} ${tx},${ty}`;
                });
                replLabels
                    .attr('x', d => (d.source.x + d.target.x) / 2)
                    .attr('y', d => (d.source.y + d.target.y) / 2 - 8);
            });

        const drag = d3.drag()
            .on('start', (ev, d) => { if (!ev.active) galaxySim.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
            .on('drag', (ev, d) => { d.fx = ev.x; d.fy = ev.y; })
            .on('end', (ev, d) => { if (!ev.active) galaxySim.alphaTarget(0); d.fx = null; d.fy = null; });
        nodeEls.call(drag);

        function replicationLabel(d) {
            const kind = d.type || 'replica';
            if (!d.details) return kind;
            const label = `${kind}: ${d.details}`;
            return label.length > 52 ? label.slice(0, 51) + '…' : label;
        }
    }

    // ---- Open DB ----
    async function openDb(db) {
        currentDbId = db.id;
        showSchema(db.name);
        try {
            const r = await fetch(`/api/databases/${db.id}/schema`);
            const d = await r.json();
            if (!r.ok) throw new Error(d.error || 'Failed');
            currentSchema = d;
            requestAnimationFrame(() => requestAnimationFrame(() => renderSchema(d)));
        } catch (e) { showGallery(); console.error(e); }
    }

    // ---- Schema ----
    function renderSchema(schema) {
        d3.select('#schema-svg').selectAll('*').remove();
        const el = document.getElementById('schema-svg');
        const W = el.clientWidth || window.innerWidth;
        const H = el.clientHeight || (window.innerHeight - 60);

        const svg = d3.select('#schema-svg').attr('viewBox', [0, 0, W, H]);
        const defs = svg.append('defs');

        // Shadow
        const sf = defs.append('filter').attr('id', 'tshadow')
            .attr('x', '-5%').attr('y', '-5%').attr('width', '115%').attr('height', '120%');
        sf.append('feDropShadow').attr('dx', 2).attr('dy', 2).attr('stdDeviation', 2).attr('flood-opacity', 0.18);

        // Arrow
        defs.append('marker').attr('id', 'arr').attr('viewBox', '0 -5 10 10')
            .attr('refX', 8).attr('refY', 0).attr('markerWidth', 7).attr('markerHeight', 7).attr('orient', 'auto')
            .append('path').attr('d', 'M0,-5L10,0L0,5').attr('fill', '#0054E3').attr('fill-opacity', 0.5);

        // Table color palette
        const TABLE_COLORS = ['#2663C9', '#2E8B57', '#D4782F', '#7B4BB3', '#1A8A8A', '#C0392B'];
        const numColors = TABLE_COLORS.length;

        const root = svg.append('g');
        const zoom = d3.zoom().scaleExtent([0.08, 4])
            .on('zoom', ev => root.attr('transform', ev.transform));
        svg.call(zoom);

        const PAD = 10, HDR = 34, ROW = 18, MIN_W = 150;

        // Flatten tables from all databases.
        const allTables = [];
        const dbNames = [];
        (schema.databases || []).forEach(db => {
            if (!dbNames.includes(db.name)) dbNames.push(db.name);
            (db.tables || []).forEach(t => allTables.push({ ...t, _db: db.name }));
        });

        // Database color palette.
        const DB_COLORS = ['#2663C9', '#2E8B57', '#D4782F', '#7B4BB3', '#1A8A8A', '#C0392B', '#E67E22', '#8E44AD'];

        const tN = allTables.map(t => {
            const dbIdx = dbNames.indexOf(t._db);
            const ml = Math.max(t.name.length, ...t.columns.map(c => c.name.length + c.dataType.length + 3));
            const w = Math.max(MIN_W, ml * 7 + PAD * 2 + 26);
            const h = HDR + t.columns.length * ROW + PAD;
            return { ...t, id: t._db + '.' + t.name, width: w, height: h, x: 0, y: 0, _dbIdx: dbIdx };
        });

        const cr = Math.min(W, H) * 0.3;
        tN.forEach((n, i) => {
            const a = (2 * Math.PI * i) / tN.length - Math.PI / 2;
            n.x = W / 2 + Math.cos(a) * cr;
            n.y = H / 2 + Math.sin(a) * cr;
        });

        const tMap = {}; tN.forEach(t => { tMap[t.id] = t; tMap[t.name] = tMap[t.name] || t; });
        const links = [];
        allTables.forEach(t => {
            (t.foreignKeys || []).forEach(fk => {
                const sourceId = (t._db || t.database) + '.' + t.name;
                // Try same-db FK first, then cross-db.
                const targetId = (t._db || t.database) + '.' + fk.referencedTable;
                const target = tMap[targetId] || tMap[fk.referencedTable];
                if (target) links.push({ source: sourceId, target: target.id });
            });
        });

        const linkG = root.append('g');
        const linkEls = linkG.selectAll('.fk-link').data(links).enter()
            .append('path').attr('class', 'fk-link')
            .attr('data-from', d => d.source).attr('data-to', d => d.target)
            .attr('marker-end', 'url(#arr)');

        const nodeG = root.append('g');
        const nodeEls = nodeG.selectAll('.table-node').data(tN).enter()
            .append('g').attr('class', 'table-node').attr('data-table', d => d.name)
            .attr('transform', d => `translate(${d.x},${d.y})`)
            .on('click', (ev, d) => { ev.stopPropagation(); selectTable(d.name); });

        const nd = d3.drag()
            .on('start', function (ev, d) { if (!ev.active) sim.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; d3.select(this).raise(); })
            .on('drag', (ev, d) => { d.fx = ev.x; d.fy = ev.y; })
            .on('end', (ev, d) => { if (!ev.active) sim.alphaTarget(0); d.fx = null; d.fy = null; });
        nodeEls.call(nd);

        // XP window-style table card
        nodeEls.append('rect').attr('class', 'table-bg')
            .attr('width', d => d.width).attr('height', d => d.height)
            .attr('filter', 'url(#tshadow)');

        // Colored header bar — color by database
        nodeEls.append('rect')
            .attr('width', d => d.width).attr('height', HDR)
            .attr('rx', '3 3 0 0')
            .attr('fill', d => DB_COLORS[d._dbIdx % DB_COLORS.length]);

        // Database label (small)
        nodeEls.append('text').attr('class', 'table-db-label')
            .attr('x', PAD).attr('y', 12)
            .attr('fill', 'rgba(255,255,255,0.7)').attr('font-size', '9px')
            .text(d => dbNames.length > 1 ? d._db : '');

        // Title
        nodeEls.append('text').attr('class', 'table-title')
            .attr('x', PAD).attr('y', HDR / 2 + 8).text(d => d.name);

        // Columns
        nodeEls.each(function (td) {
            const g = d3.select(this);
            const fkc = new Set((td.foreignKeys || []).map(fk => fk.columnName));
            td.columns.forEach((col, ci) => {
                const y = HDR + ci * ROW + ROW / 2 + 4;
                if (ci === 0) g.append('line').attr('x1', 1).attr('x2', td.width - 1).attr('y1', HDR).attr('y2', HDR).attr('stroke', '#ACA899');

                // Alternating row bg
                if (ci % 2 === 1) g.append('rect').attr('x', 1).attr('y', HDR + ci * ROW).attr('width', td.width - 2).attr('height', ROW).attr('fill', '#F5F3EB');

                if (col.isPrimaryKey) g.append('text').attr('class', 'column-icon pk').attr('x', PAD).attr('y', y).text('🔑');
                else if (fkc.has(col.name)) g.append('text').attr('class', 'column-icon fk').attr('x', PAD).attr('y', y).text('🔗');
                g.append('text').attr('class', 'column-name').attr('x', PAD + 16).attr('y', y).attr('data-col', col.name).text(col.name);
                g.append('text').attr('class', 'column-type').attr('x', td.width - PAD).attr('y', y).attr('text-anchor', 'end').text(col.dataType);
            });
        });

        const sim = d3.forceSimulation(tN)
            .force('center', d3.forceCenter(W / 2, H / 2))
            .force('charge', d3.forceManyBody().strength(-800))
            .force('link', d3.forceLink(links).id(d => d.id || d.name || d).distance(240).strength(0.4))
            .force('collision', d3.forceCollide().radius(d => Math.max(d.width, d.height) / 2 + 20))
            .force('x', d3.forceX(W / 2).strength(0.03))
            .force('y', d3.forceY(H / 2).strength(0.03))
            .alphaDecay(0.018)
            .on('tick', () => {
                nodeEls.attr('transform', d => `translate(${d.x - d.width / 2},${d.y - d.height / 2})`);
                updateLinks();
            });

        function updateLinks() {
            linkEls.attr('d', d => {
                const s = typeof d.source === 'object' ? d.source : tMap[d.source];
                const t = typeof d.target === 'object' ? d.target : tMap[d.target];
                if (!s || !t) return '';
                const dx = t.x - s.x, dy = t.y - s.y;
                const dist = Math.sqrt(dx * dx + dy * dy) || 1;
                const sClip = clipToBox(s.x, s.y, s.width, s.height, dx / dist, dy / dist);
                const tClip = clipToBox(t.x, t.y, t.width, t.height, -dx / dist, -dy / dist);
                const mx = (sClip.x + tClip.x) / 2, my = (sClip.y + tClip.y) / 2;
                const off = dist * 0.1;
                const nx = -(tClip.y - sClip.y) / dist, ny = (tClip.x - sClip.x) / dist;
                return `M${sClip.x},${sClip.y} Q${mx + nx * off},${my + ny * off} ${tClip.x},${tClip.y}`;
            });
        }

        function clipToBox(cx, cy, w, h, dirX, dirY) {
            const hw = w / 2, hh = h / 2;
            let tx = Infinity, ty = Infinity;
            if (dirX !== 0) tx = Math.abs(hw / dirX);
            if (dirY !== 0) ty = Math.abs(hh / dirY);
            const t = Math.min(tx, ty);
            return { x: cx + dirX * t, y: cy + dirY * t };
        }

        svg.on('click', () => { detailOverlay.hidden = true; deselectAll(); });
        setTimeout(() => fit(svg, zoom, tN, W, H), 700);
    }

    // ---- Search ----
    schemaSearch.addEventListener('input', () => {
        const q = schemaSearch.value.trim().toLowerCase();
        searchClear.hidden = !q;
        if (!q) {
            clearSearch();
            return;
        }
        if (!currentSchema) return;

        let matchCount = 0;
        const matchedTables = new Set();

        // Flatten tables from databases for search.
        const allSearchTables = [];
        (currentSchema.databases || []).forEach(db => {
            (db.tables || []).forEach(t => allSearchTables.push(t));
        });

        allSearchTables.forEach(t => {
            const tMatch = t.name.toLowerCase().includes(q);
            const matchedCols = t.columns.filter(c => c.name.toLowerCase().includes(q));
            if (tMatch || matchedCols.length > 0) {
                matchedTables.add(t.name);
                matchCount += (tMatch ? 1 : 0) + matchedCols.length;
            }
        });

        // Apply highlighting to SVG nodes
        document.querySelectorAll('.table-node').forEach(el => {
            const name = el.dataset.table;
            if (matchedTables.has(name)) {
                el.classList.add('search-match');
                el.classList.remove('search-dim');
                // Highlight matched columns
                el.querySelectorAll('.column-name').forEach(cn => {
                    if (cn.dataset.col && cn.dataset.col.toLowerCase().includes(q)) {
                        cn.classList.add('search-hit');
                    } else {
                        cn.classList.remove('search-hit');
                    }
                });
            } else {
                el.classList.remove('search-match');
                el.classList.add('search-dim');
                el.querySelectorAll('.column-name').forEach(cn => cn.classList.remove('search-hit'));
            }
        });

        searchCount.textContent = matchCount + ' match' + (matchCount !== 1 ? 'es' : '');
        searchCount.hidden = false;
    });

    searchClear.addEventListener('click', () => {
        schemaSearch.value = '';
        clearSearch();
        schemaSearch.focus();
    });

    function clearSearch() {
        document.querySelectorAll('.table-node').forEach(el => {
            el.classList.remove('search-match', 'search-dim');
            el.querySelectorAll('.column-name').forEach(cn => cn.classList.remove('search-hit'));
        });
        searchCount.hidden = true;
        searchClear.hidden = true;
    }

    // ---- Selection ----
    function selectTable(name) {
        if (!currentSchema) return;
        deselectAll();
        document.querySelectorAll('.table-node').forEach(el => { if (el.dataset.table === name) el.classList.add('highlighted'); });
        document.querySelectorAll('.fk-link').forEach(el => { if (el.dataset.from === name || el.dataset.to === name) el.classList.add('highlighted'); });
        showDetail(name);
    }

    function deselectAll() {
        document.querySelectorAll('.highlighted').forEach(el => el.classList.remove('highlighted'));
    }

    function showDetail(name) {
        // Find table across all databases.
        let t = null;
        if (currentSchema && currentSchema.databases) {
            for (const db of currentSchema.databases) {
                t = (db.tables || []).find(tbl => tbl.name === name);
                if (t) break;
            }
        }
        if (!t) return;
        detailTableName.textContent = 'Table Properties — ' + t.name;
        const fkc = new Set((t.foreignKeys || []).map(fk => fk.columnName));

        let h = '<div class="detail-section"><h4>Columns (' + t.columns.length + ')</h4>';
        t.columns.forEach(c => {
            const pk = c.isPrimaryKey, fk = fkc.has(c.name);
            h += `<div class="detail-column">
                <span class="col-icon ${pk ? 'pk' : fk ? 'fk' : ''}">${pk ? 'PK' : fk ? 'FK' : ''}</span>
                <span class="col-name">${esc(c.name)}</span>
                <span class="col-type">${esc(c.dataType)}</span>
                ${c.isNullable ? '<span class="col-nullable">NULL</span>' : ''}
            </div>`;
        });
        h += '</div>';

        if (t.foreignKeys && t.foreignKeys.length) {
            h += '<div class="detail-section"><h4>Foreign Keys (' + t.foreignKeys.length + ')</h4>';
            t.foreignKeys.forEach(fk => {
                h += `<div class="detail-fk">
                    <span class="fk-name">${esc(fk.constraintName)}</span>
                    <span class="fk-mapping">${esc(fk.columnName)}<span class="fk-arrow"> → </span>${esc(fk.referencedTable)}.${esc(fk.referencedColumn)}</span>
                </div>`;
            });
            h += '</div>';
        }

        if (t.indexes && t.indexes.length) {
            h += '<div class="detail-section"><h4>Indexes (' + t.indexes.length + ')</h4>';
            t.indexes.forEach(idx => {
                h += `<div class="detail-index">
                    <span class="idx-name">${esc(idx.name)}</span>
                    <span class="idx-cols">(${idx.columns.map(esc).join(', ')})</span>
                    ${idx.isUnique ? '<span class="idx-unique">UNIQUE</span>' : ''}
                </div>`;
            });
            h += '</div>';
        }

        if (t.checkConstraints && t.checkConstraints.length) {
            h += '<div class="detail-section"><h4>Check Constraints (' + t.checkConstraints.length + ')</h4>';
            t.checkConstraints.forEach(chk => {
                h += `<div class="detail-check">
                    <span class="chk-name">${esc(chk.name)}</span>
                    <span class="chk-expr">${esc(chk.expression)}</span>
                </div>`;
            });
            h += '</div>';
        }

        detailContent.innerHTML = h;
        detailOverlay.hidden = false;
    }

    closeDetailBtn.addEventListener('click', e => { e.stopPropagation(); detailOverlay.hidden = true; deselectAll(); });

    function fit(svg, zoom, nodes, w, h) {
        if (!nodes.length) return;
        const pad = 60;
        let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
        nodes.forEach(n => { const nx = n.x - (n.width || 0) / 2, ny = n.y - (n.height || 0) / 2; x0 = Math.min(x0, nx); y0 = Math.min(y0, ny); x1 = Math.max(x1, nx + (n.width || 100)); y1 = Math.max(y1, ny + (n.height || 60)); });
        const cw = x1 - x0 + pad * 2, ch = y1 - y0 + pad * 2;
        const s = Math.min(w / cw, h / ch, 1.2);
        svg.transition().duration(600).ease(d3.easeCubicOut)
            .call(zoom.transform, d3.zoomIdentity.translate((w - cw * s) / 2 - x0 * s + pad * s, (h - ch * s) / 2 - y0 * s + pad * s).scale(s));
    }

    function esc(s) { const d = document.createElement('div'); d.textContent = s; return d.innerHTML; }

    boot();
})();
