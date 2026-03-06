// Lightbox for image attachments with pinch-zoom and pan.
const lightbox = document.getElementById('lightbox');
if (lightbox) {
  const lightboxImg = lightbox.querySelector('.lightbox__img');
  const lightboxClose = lightbox.querySelector('.lightbox__close');
  const prevBtn = lightbox.querySelector('.lightbox__nav--prev');
  const nextBtn = lightbox.querySelector('.lightbox__nav--next');

  let images = [];
  let index = 0;

  // Zoom/pan state.
  let scale = 1, tx = 0, ty = 0;
  let lastTapTime = 0;
  let pointerMoved = false;

  // Active pointers: Map<pointerId, {x, y}>.
  const ptrs = new Map();

  // Pinch init state.
  let pinchDist0 = 0, pinchScale0 = 1;
  let pinchMid0X = 0, pinchMid0Y = 0;
  let pinchTx0 = 0, pinchTy0 = 0;

  // Single-pointer pan/swipe state.
  let p1X0 = 0, p1Y0 = 0, p1Tx0 = 0, p1Ty0 = 0;

  function applyTransform() {
    lightboxImg.style.transform = `scale(${scale}) translate(${tx}px, ${ty}px)`;
    lightboxImg.classList.toggle('lightbox__img--zoomed', scale > 1);
  }

  function clampTranslate() {
    // offsetWidth/Height are unaffected by CSS transforms — they give the natural rendered size.
    const maxTx = (lightboxImg.offsetWidth * (scale - 1)) / 2;
    const maxTy = (lightboxImg.offsetHeight * (scale - 1)) / 2;
    tx = Math.max(-maxTx, Math.min(maxTx, tx));
    ty = Math.max(-maxTy, Math.min(maxTy, ty));
  }

  function resetTransform() {
    scale = 1; tx = 0; ty = 0;
    pointerMoved = false;
    ptrs.clear();
    applyTransform();
  }

  function show(i) {
    index = i;
    lightboxImg.src = images[index].src;
    lightboxImg.alt = images[index].alt;
    prevBtn.hidden = images.length < 2;
    nextBtn.hidden = images.length < 2;
    prevBtn.disabled = index === 0;
    nextBtn.disabled = index === images.length - 1;
    resetTransform();
  }

  function prev() { if (index > 0) show(index - 1); }
  function next() { if (index < images.length - 1) show(index + 1); }

  document.addEventListener('click', (e) => {
    const img = e.target.closest('.message__media-img');
    if (!img) return;
    e.preventDefault();
    const article = img.closest('article.message');
    images = article
      ? Array.from(article.querySelectorAll('.message__media-img'))
      : [img];
    show(images.indexOf(img));
    lightbox.showModal();
  });

  lightboxClose.addEventListener('click', () => lightbox.close());
  prevBtn.addEventListener('click', prev);
  nextBtn.addEventListener('click', next);

  // Backdrop click closes. Suppressed if pointer moved during a gesture.
  lightbox.addEventListener('pointerdown', (e) => {
    if (e.target === lightbox) pointerMoved = false;
  });
  lightbox.addEventListener('click', (e) => {
    if (e.target === lightbox && !pointerMoved) lightbox.close();
  });

  // Keyboard navigation (arrow keys + hjkl).
  lightbox.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowLeft' || e.key === 'h') { e.preventDefault(); prev(); }
    else if (e.key === 'ArrowRight' || e.key === 'l') { e.preventDefault(); next(); }
  });

  // Wheel: zoom toward cursor.
  lightbox.addEventListener('wheel', (e) => {
    e.preventDefault();
    const prevScale = scale;
    scale = Math.max(1, Math.min(8, scale * (e.deltaY < 0 ? 1.1 : 0.9)));
    if (scale === prevScale) return;
    const rect = lightboxImg.getBoundingClientRect();
    const cx = e.clientX - (rect.left + rect.width / 2);
    const cy = e.clientY - (rect.top + rect.height / 2);
    tx += cx * (1 / scale - 1 / prevScale);
    ty += cy * (1 / scale - 1 / prevScale);
    clampTranslate();
    applyTransform();
  }, { passive: false });

  // Pointer events on the image for pinch, pan, and swipe.
  lightboxImg.addEventListener('pointerdown', (e) => {
    e.preventDefault();
    if (ptrs.size === 0) pointerMoved = false;
    lightboxImg.setPointerCapture(e.pointerId);
    ptrs.set(e.pointerId, { x: e.clientX, y: e.clientY });

    // Double-tap to toggle zoom (touch only).
    if (e.pointerType === 'touch' && ptrs.size === 1) {
      const now = Date.now();
      if (now - lastTapTime < 300) {
        lastTapTime = 0;
        if (scale > 1) {
          resetTransform();
        } else {
          // Zoom to 2.5× keeping tap point fixed. At scale=1, tx=0 the
          // element-local offset equals the viewport offset from dialog center.
          const cx = e.clientX - window.innerWidth / 2;
          const cy = e.clientY - window.innerHeight / 2;
          scale = 2.5;
          tx = cx * (1 / scale - 1);
          ty = cy * (1 / scale - 1);
          clampTranslate();
          applyTransform();
        }
        return;
      }
      lastTapTime = now;
    }

    if (ptrs.size === 2) {
      const [a, b] = [...ptrs.values()];
      pinchDist0 = Math.hypot(b.x - a.x, b.y - a.y);
      pinchScale0 = scale;
      pinchMid0X = (a.x + b.x) / 2;
      pinchMid0Y = (a.y + b.y) / 2;
      pinchTx0 = tx;
      pinchTy0 = ty;
    } else if (ptrs.size === 1) {
      p1X0 = e.clientX; p1Y0 = e.clientY;
      p1Tx0 = tx; p1Ty0 = ty;
    }
  });

  lightboxImg.addEventListener('pointermove', (e) => {
    if (!ptrs.has(e.pointerId)) return;
    ptrs.set(e.pointerId, { x: e.clientX, y: e.clientY });

    if (ptrs.size === 2) {
      const [a, b] = [...ptrs.values()];
      const dist = Math.hypot(b.x - a.x, b.y - a.y);
      const newScale = Math.max(1, Math.min(8, pinchScale0 * (dist / pinchDist0)));
      const midX = (a.x + b.x) / 2;
      const midY = (a.y + b.y) / 2;
      // Keep the initial pinch midpoint anchored on the same image content.
      // Using dialog center (≈ window center) as transform origin OX/OY.
      const OX = window.innerWidth / 2;
      const OY = window.innerHeight / 2;
      const pxMid = (pinchMid0X - OX) / pinchScale0 - pinchTx0;
      const pyMid = (pinchMid0Y - OY) / pinchScale0 - pinchTy0;
      tx = (midX - OX) / newScale - pxMid;
      ty = (midY - OY) / newScale - pyMid;
      scale = newScale;
      clampTranslate();
      applyTransform();
      pointerMoved = true;
    } else if (ptrs.size === 1) {
      const dx = e.clientX - p1X0;
      const dy = e.clientY - p1Y0;
      if (Math.abs(dx) > 5 || Math.abs(dy) > 5) pointerMoved = true;
      if (scale > 1) {
        tx = p1Tx0 + dx / scale;
        ty = p1Ty0 + dy / scale;
        clampTranslate();
        applyTransform();
      }
    }
  });

  lightboxImg.addEventListener('pointerup', (e) => {
    const hadCount = ptrs.size;
    ptrs.delete(e.pointerId);

    // Swipe to navigate (single pointer at 1× zoom).
    if (hadCount === 1 && scale === 1) {
      const dx = e.clientX - p1X0;
      if (Math.abs(dx) > 40) dx < 0 ? next() : prev();
    }

    // Transition 2→1 finger: reinit single-pointer state from remaining pointer.
    if (hadCount === 2 && ptrs.size === 1) {
      const [pos] = [...ptrs.values()];
      p1X0 = pos.x; p1Y0 = pos.y;
      p1Tx0 = tx; p1Ty0 = ty;
    }
  });

  lightboxImg.addEventListener('pointercancel', (e) => {
    ptrs.delete(e.pointerId);
  });
}
