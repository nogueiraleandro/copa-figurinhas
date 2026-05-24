/**
 * mascote.js — Orquestrador da arara animada (Copa Figurinhas 2026)
 * Auto-inicializa ao carregar. Expõe window.mascotPlay() e window.confettiBurst().
 * Respeita prefers-reduced-motion. Fallback puro-CSS/canvas se Lottie falhar.
 */
(function () {
  'use strict';

  var STATIC = '/static/lottie/';
  var reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  /* ── DOM helpers ─────────────────────────────────────────── */
  function el(tag, cls, styles) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (styles) Object.assign(e.style, styles);
    return e;
  }

  /* ── Mascot container ────────────────────────────────────── */
  var mascoteEl = el('div', 'mascote');
  document.body.appendChild(mascoteEl);

  /* ── Lottie overlay for full-screen bursts ───────────────── */
  var overlayEl = el('div', 'lottie-overlay');
  document.body.appendChild(overlayEl);

  /* ── State ───────────────────────────────────────────────── */
  var lottieLoaded = false;
  var idleAnim = null;
  var eventAnim = null;
  var confettiAnim = null;
  var fallbackMode = false;

  /* ── Canvas confetti fallback (adapted from tv.html) ─────── */
  function canvasConfettiBurst() {
    var canvas = el('canvas', 'confetti-canvas');
    document.body.appendChild(canvas);
    var ctx = canvas.getContext('2d');
    function resize() { canvas.width = window.innerWidth; canvas.height = window.innerHeight; }
    resize();
    var colors = ['#f4d03f','#e74c3c','#3498db','#2ecc71','#9b59b6','#e67e22','#ffffff','#ffdf00'];
    var pieces = [];
    for (var i = 0; i < 120; i++) {
      pieces.push({
        x: Math.random() * canvas.width,
        y: -20 - Math.random() * canvas.height * 0.3,
        w: 6 + Math.random() * 8,
        h: 8 + Math.random() * 10,
        color: colors[(Math.random() * colors.length) | 0],
        vy: 2 + Math.random() * 3,
        vx: -1.5 + Math.random() * 3,
        rot: Math.random() * Math.PI,
        vr: -0.12 + Math.random() * 0.24
      });
    }
    var frame = 0;
    var maxFrames = 180;
    function draw() {
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      for (var p of pieces) {
        p.y += p.vy; p.x += p.vx; p.rot += p.vr;
        if (p.y > canvas.height + 20) p.y = -20;
        ctx.save();
        ctx.globalAlpha = Math.min(1, (maxFrames - frame) / 60);
        ctx.translate(p.x, p.y);
        ctx.rotate(p.rot);
        ctx.fillStyle = p.color;
        ctx.fillRect(-p.w / 2, -p.h / 2, p.w, p.h);
        ctx.restore();
      }
      frame++;
      if (frame < maxFrames) {
        requestAnimationFrame(draw);
      } else {
        canvas.remove();
      }
    }
    draw();
  }

  /* ── Emoji fallback mascot ───────────────────────────────── */
  function showEmojiFallback() {
    mascoteEl.innerHTML = '<span class="mascote-emoji">&#x1F99C;</span>';
    fallbackMode = true;
  }

  /* ── Lottie init ─────────────────────────────────────────── */
  function initLottie() {
    if (typeof lottie === 'undefined') {
      showEmojiFallback();
      return;
    }
    lottieLoaded = true;

    /* mascot idle */
    try {
      idleAnim = lottie.loadAnimation({
        container: mascoteEl,
        renderer: 'svg',
        loop: true,
        autoplay: !reducedMotion,
        path: STATIC + 'arara-idle.json'
      });
      idleAnim.addEventListener('data_failed', function() {
        showEmojiFallback();
      });
    } catch (e) {
      showEmojiFallback();
    }
  }

  /* ── mascotPlay(event) ───────────────────────────────────── */
  window.mascotPlay = function (event) {
    if (reducedMotion) return;

    var fileMap = {
      collect:  'arara-collect.json',
      repeat:   'arara-repeat.json',
      complete: 'arara-collect.json'  /* reusar collect para complete */
    };
    var file = fileMap[event] || 'arara-idle.json';

    if (fallbackMode) {
      /* emoji bounce via CSS class */
      mascoteEl.classList.remove('mascote-bounce');
      void mascoteEl.offsetWidth; /* reflow to restart */
      mascoteEl.classList.add('mascote-bounce');
      return;
    }

    if (!lottieLoaded) return;

    /* stop previous event anim */
    if (eventAnim) { try { eventAnim.destroy(); } catch(e) {} eventAnim = null; }

    /* pause idle */
    if (idleAnim) idleAnim.pause();

    try {
      eventAnim = lottie.loadAnimation({
        container: mascoteEl,
        renderer: 'svg',
        loop: false,
        autoplay: true,
        path: STATIC + file
      });

      eventAnim.addEventListener('complete', function () {
        if (eventAnim) { try { eventAnim.destroy(); } catch(e) {} eventAnim = null; }
        if (idleAnim) {
          /* restart idle clean */
          idleAnim.stop();
          idleAnim.play();
        }
      });

      eventAnim.addEventListener('data_failed', function () {
        eventAnim = null;
        if (idleAnim) idleAnim.play();
      });
    } catch (e) {
      if (idleAnim) idleAnim.play();
    }
  };

  /* ── confettiBurst() ─────────────────────────────────────── */
  window.confettiBurst = function () {
    if (reducedMotion) return;

    if (!lottieLoaded) {
      canvasConfettiBurst();
      return;
    }

    /* destroy previous confetti */
    if (confettiAnim) { try { confettiAnim.destroy(); } catch(e) {} confettiAnim = null; }
    overlayEl.innerHTML = '';
    overlayEl.style.display = 'block';

    try {
      confettiAnim = lottie.loadAnimation({
        container: overlayEl,
        renderer: 'svg',
        loop: false,
        autoplay: true,
        path: STATIC + 'confetti.json'
      });

      confettiAnim.addEventListener('complete', function () {
        overlayEl.style.display = 'none';
        overlayEl.innerHTML = '';
        confettiAnim = null;
      });

      confettiAnim.addEventListener('data_failed', function () {
        overlayEl.style.display = 'none';
        confettiAnim = null;
        canvasConfettiBurst(); /* fallback */
      });
    } catch (e) {
      overlayEl.style.display = 'none';
      canvasConfettiBurst();
    }
  };

  /* ── trophyBurst() — confetti + trophy overlay ───────────── */
  window.trophyBurst = function () {
    if (reducedMotion) return;
    if (!lottieLoaded) {
      canvasConfettiBurst();
      return;
    }

    if (confettiAnim) { try { confettiAnim.destroy(); } catch(e) {} confettiAnim = null; }
    overlayEl.innerHTML = '';
    overlayEl.style.display = 'block';

    try {
      confettiAnim = lottie.loadAnimation({
        container: overlayEl,
        renderer: 'svg',
        loop: false,
        autoplay: true,
        path: STATIC + 'trophy.json'
      });

      confettiAnim.addEventListener('complete', function () {
        /* After trophy, fire confetti */
        confettiAnim = null;
        overlayEl.innerHTML = '';
        window.confettiBurst();
      });

      confettiAnim.addEventListener('data_failed', function () {
        confettiAnim = null;
        overlayEl.style.display = 'none';
        canvasConfettiBurst();
      });
    } catch (e) {
      overlayEl.style.display = 'none';
      canvasConfettiBurst();
    }
  };

  /* ── Load lottie.min.js dynamically ─────────────────────── */
  var script = document.createElement('script');
  script.src = STATIC + 'lottie.min.js';
  script.onload = function () { initLottie(); };
  script.onerror = function () { showEmojiFallback(); };
  document.head.appendChild(script);

})();
