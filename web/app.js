/* ============================================================
   DB Barrel 2.0 — Windows XP Light Mode (v5)
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

    let databases = [];
    let currentSchema = null;
    let galaxySim = null;

    // XP DB type colors
    const DB_CLR = {
        postgresql: { fill: '#B8D4F0', stroke: '#336791', text: '#003366' },
        mysql: { fill: '#FFF0D0', stroke: '#F29111', text: '#8B4500' },
        mariadb: { fill: '#F0E0D4', stroke: '#C0765A', text: '#5E3322' },
        sqlite: { fill: '#D0E8F8', stroke: '#4F9CD0', text: '#1A4970' },
    };
    const DEF_CLR = { fill: '#E0E0E0', stroke: '#888', text: '#333' };
    function clr(d) { return DB_CLR[d] || DEF_CLR; }

    // ---- Navigation ----
    function showGallery() {
        schemaScreen.hidden = true;
        galaxyScreen.hidden = false;
        galaxyScreen.classList.add('fade-in');
        currentDbLabel.hidden = true;
        currentSchema = null;
        detailOverlay.hidden = true;
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

    // ---- Load ----
    async function boot() {
        try {
            const r = await fetch('/api/databases');
            databases = await r.json();
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

        const root = svg.append('g');

        // ZOOM on galaxy
        const zoom = d3.zoom()
            .scaleExtent([0.3, 3])
            .on('zoom', ev => root.attr('transform', ev.transform));
        svg.call(zoom);

        const NODE_W = 142, NODE_H = 148, ICON_SIZE = 90;
        const nodes = dbs.map((db, i) => ({
            ...db, index: i,
            x: W / 2 + (Math.random() - 0.5) * 300,
            y: H / 2 + (Math.random() - 0.5) * 200,
        }));

        // Orbit lines
        const orbits = root.append('g');
        for (let i = 0; i < nodes.length; i++)
            for (let j = i + 1; j < nodes.length; j++)
                orbits.append('line').attr('class', 'orbit-line').attr('data-i', i).attr('data-j', j);

        const grp = root.append('g');
        const nodeEls = grp.selectAll('.db-node').data(nodes).enter().append('g')
            .attr('class', d => 'db-node' + (d.status === 'error' ? ' error-node' : ''))
            .attr('transform', d => `translate(${d.x},${d.y})`);

        // XP-style card shadow
        nodeEls.append('rect')
            .attr('class', 'node-shadow')
            .attr('x', -NODE_W / 2 + 3).attr('y', -28 + 3)
            .attr('width', NODE_W).attr('height', NODE_H)
            .attr('rx', 3).attr('fill', 'rgba(0,0,0,0.08)').attr('opacity', 0);

        // Card background
        nodeEls.append('rect')
            .attr('class', 'node-border')
            .attr('x', -NODE_W / 2).attr('y', -28)
            .attr('width', NODE_W).attr('height', NODE_H)
            .attr('rx', 3)
            .attr('fill', d => clr(d.driver).fill)
            .attr('stroke', d => clr(d.driver).stroke)
            .attr('stroke-width', 2)
            .attr('filter', 'url(#xp-shadow)');

        // Card titlebar (mini XP-style header)
        nodeEls.append('rect')
            .attr('x', -NODE_W / 2 + 1).attr('y', -27)
            .attr('width', NODE_W - 2).attr('height', 16)
            .attr('rx', 2)
            .attr('fill', d => clr(d.driver).stroke)
            .attr('opacity', 0.15);

        // DB logo — centered between titlebar (y=-10) and labels (y~88)
        nodeEls.append('image')
            .attr('href', d => `svg/${d.driver}.svg`)
            .attr('x', -ICON_SIZE / 2).attr('y', -16)
            .attr('width', ICON_SIZE).attr('height', ICON_SIZE)
            .style('pointer-events', 'none');

        // Status icon (green checkmark or red X)
        nodeEls.append('text')
            .attr('x', NODE_W / 2 - 14).attr('y', -12)
            .attr('font-size', '13px')
            .attr('text-anchor', 'middle')
            .text(d => d.status === 'ok' ? '✔' : '✘')
            .attr('fill', d => d.status === 'ok' ? '#2E8B57' : '#CC0000');

        // Name — tight under the logo
        nodeEls.append('text').attr('class', 'node-label')
            .attr('y', 88).text(d => d.name);

        // Driver badge
        nodeEls.append('text').attr('class', 'node-driver')
            .attr('y', 102)
            .attr('fill', d => clr(d.driver).text)
            .text(d => d.driver);

        // Error label only
        nodeEls.filter(d => d.status === 'error').each(function (d) {
            d3.select(this).append('text').attr('class', 'node-error').attr('y', NODE_H - 24).text('Error');
        });

        // Click
        nodeEls.filter(d => d.status === 'ok')
            .on('click', (ev, d) => { ev.stopPropagation(); openDb(d); })
            .style('cursor', 'pointer');

        // Force
        galaxySim = d3.forceSimulation(nodes)
            .force('center', d3.forceCenter(W / 2, H / 2))
            .force('charge', d3.forceManyBody().strength(-500))
            .force('collision', d3.forceCollide().radius(90))
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
            });

        const drag = d3.drag()
            .on('start', (ev, d) => { if (!ev.active) galaxySim.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
            .on('drag', (ev, d) => { d.fx = ev.x; d.fy = ev.y; })
            .on('end', (ev, d) => { if (!ev.active) galaxySim.alphaTarget(0); d.fx = null; d.fy = null; });
        nodeEls.call(drag);
    }

    // ---- Open DB ----
    async function openDb(db) {
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

        // Table color palette (flat matte)
        const TABLE_COLORS = ['#2663C9', '#2E8B57', '#D4782F', '#7B4BB3', '#1A8A8A', '#C0392B'];
        const numColors = TABLE_COLORS.length;

        const root = svg.append('g');
        const zoom = d3.zoom().scaleExtent([0.08, 4])
            .on('zoom', ev => root.attr('transform', ev.transform));
        svg.call(zoom);

        const PAD = 10, HDR = 24, ROW = 18, MIN_W = 150;
        const tN = schema.tables.map(t => {
            const ml = Math.max(t.name.length, ...t.columns.map(c => c.name.length + c.dataType.length + 3));
            const w = Math.max(MIN_W, ml * 7 + PAD * 2 + 26);
            const h = HDR + t.columns.length * ROW + PAD;
            return { ...t, id: t.name, width: w, height: h, x: 0, y: 0 };
        });

        const cr = Math.min(W, H) * 0.3;
        tN.forEach((n, i) => {
            const a = (2 * Math.PI * i) / tN.length - Math.PI / 2;
            n.x = W / 2 + Math.cos(a) * cr;
            n.y = H / 2 + Math.sin(a) * cr;
        });

        const tMap = {}; tN.forEach(t => { tMap[t.name] = t; });
        const links = [];
        schema.tables.forEach(t => {
            (t.foreignKeys || []).forEach(fk => {
                if (tMap[fk.referencedTable]) links.push({ source: t.name, target: fk.referencedTable });
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

        // Colored header bar (flat matte, cycles through palette)
        nodeEls.append('rect')
            .attr('width', d => d.width).attr('height', HDR)
            .attr('fill', (d, i) => TABLE_COLORS[i % numColors]);

        // Title
        nodeEls.append('text').attr('class', 'table-title')
            .attr('x', PAD).attr('y', HDR / 2 + 4).text(d => d.name);

        // Columns
        nodeEls.each(function (td) {
            const g = d3.select(this);
            const fkc = new Set((td.foreignKeys || []).map(fk => fk.columnName));
            td.columns.forEach((col, ci) => {
                const y = HDR + ci * ROW + ROW / 2 + 4;
                if (ci === 0) g.append('line').attr('x1', 1).attr('x2', td.width - 1).attr('y1', HDR).attr('y2', HDR).attr('stroke', '#ACA899');

                // Alternating row bg (inset to stay inside border)
                if (ci % 2 === 1) g.append('rect').attr('x', 1).attr('y', HDR + ci * ROW).attr('width', td.width - 2).attr('height', ROW).attr('fill', '#F5F3EB');

                if (col.isPrimaryKey) g.append('text').attr('class', 'column-icon pk').attr('x', PAD).attr('y', y).text('🔑');
                else if (fkc.has(col.name)) g.append('text').attr('class', 'column-icon fk').attr('x', PAD).attr('y', y).text('🔗');
                g.append('text').attr('class', 'column-name').attr('x', PAD + 16).attr('y', y).text(col.name);
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
                // Clip to box edges
                const sClip = clipToBox(s.x, s.y, s.width, s.height, dx / dist, dy / dist);
                const tClip = clipToBox(t.x, t.y, t.width, t.height, -dx / dist, -dy / dist);
                const mx = (sClip.x + tClip.x) / 2, my = (sClip.y + tClip.y) / 2;
                const off = dist * 0.1;
                const nx = -(tClip.y - sClip.y) / dist, ny = (tClip.x - sClip.x) / dist;
                return `M${sClip.x},${sClip.y} Q${mx + nx * off},${my + ny * off} ${tClip.x},${tClip.y}`;
            });
        }

        function clipToBox(cx, cy, w, h, dirX, dirY) {
            // Find intersection of ray from center in direction (dirX, dirY) with box edges
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
        const t = currentSchema.tables.find(tbl => tbl.name === name);
        if (!t) return;
        detailTableName.textContent = 'Table Properties - ' + t.name;
        const fkc = new Set((t.foreignKeys || []).map(fk => fk.columnName));
        let h = '<div class="detail-section"><h4>Columns</h4>';
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
            h += '<div class="detail-section"><h4>Foreign Keys</h4>';
            t.foreignKeys.forEach(fk => {
                h += `<div class="detail-fk">
                    <span class="fk-name">${esc(fk.constraintName)}</span>
                    <span class="fk-mapping">${esc(fk.columnName)}<span class="fk-arrow"> → </span>${esc(fk.referencedTable)}.${esc(fk.referencedColumn)}</span>
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
