// Media upload (paste + drag-and-drop + file picker).
// Uploads files directly to S3 via a presigned PUT URL and queues them as
// attachment chips on the form.
const ALLOWED_TYPES = {
  'image/jpeg': true,
  'image/png': true,
  'image/gif': true,
  'image/webp': true,
  'video/mp4': true,
  'video/webm': true,
};
const ta = document.querySelector('.message-form__textarea');
const form = document.querySelector('.message-form');
const previewsEl = document.getElementById('attachment-previews');
const inputEl = document.getElementById('attachment-input');
const fileInput = document.getElementById('file-input');
if (ta && form && previewsEl && inputEl) {
  let pendingAttachments = []; // [{url, content_type, filename}]
  let uploadCount = 0; // number of uploads currently in-flight

  function syncInput() {
    inputEl.value = pendingAttachments.length ? JSON.stringify(pendingAttachments) : '';
  }

  function setSendDisabled(disabled) {
    const btn = form.querySelector('.message-form__send');
    if (btn) btn.disabled = disabled;
  }

  // Generate a 12-character random hex string to use as the file's key stem.
  function randomHex(len) {
    const arr = new Uint8Array(Math.ceil(len / 2));
    crypto.getRandomValues(arr);
    return Array.from(arr, (b) => b.toString(16).padStart(2, '0'))
      .join('')
      .slice(0, len);
  }

  // Build a preview chip element for a pending upload.
  function makeChip(contentType, objectURL) {
    const chip = document.createElement('div');
    chip.className = 'attachment-chip';

    if (contentType.startsWith('image/')) {
      const thumb = document.createElement('img');
      thumb.src = objectURL;
      thumb.alt = '';
      chip.appendChild(thumb);
    } else {
      const icon = document.createElement('span');
      icon.className = 'attachment-chip__icon';
      icon.textContent = '\uD83C\uDFA5'; // 🎥
      chip.appendChild(icon);
    }

    const spinner = document.createElement('span');
    spinner.className = 'attachment-chip__spinner';
    chip.appendChild(spinner);

    const removeBtn = document.createElement('button');
    removeBtn.type = 'button';
    removeBtn.className = 'attachment-chip__remove';
    removeBtn.setAttribute('aria-label', 'Remove attachment');
    removeBtn.textContent = '\u00D7'; // ×
    removeBtn.addEventListener('click', () => {
      const idx = parseInt(chip.dataset.attachmentIdx, 10);
      if (!Number.isNaN(idx)) {
        pendingAttachments.splice(idx, 1);
        // Re-index remaining chips.
        const chips = previewsEl.querySelectorAll('.attachment-chip[data-attachment-idx]');
        let counter = 0;
        chips.forEach((c) => {
          c.dataset.attachmentIdx = counter++;
        });
        syncInput();
      }
      chip.remove();
      if (previewsEl.children.length === 0) previewsEl.hidden = true;
    });
    chip.appendChild(removeBtn);

    previewsEl.hidden = false;
    previewsEl.appendChild(chip);
    return chip;
  }

  // Upload a single File object: presign → PUT → chip.
  function uploadFile(file) {
    const objectURL = URL.createObjectURL(file);
    const chip = makeChip(file.type, objectURL);

    uploadCount++;
    setSendDisabled(true);

    const hash = randomHex(12);
    const params = new URLSearchParams({
      hash: hash,
      content_type: file.type,
      content_length: file.size,
    });

    fetch(`/rooms/${window.roomID}/upload-url?${params}`, { credentials: 'same-origin' })
      .then((r) => {
        if (!r.ok) throw new Error(`presign failed: ${r.status}`);
        return r.json();
      })
      .then((data) =>
        fetch(data.upload_url, {
          method: 'PUT',
          headers: { 'Content-Type': file.type },
          body: file,
        }).then((r) => {
          if (!r.ok) throw new Error(`upload failed: ${r.status}`);
          return data.public_url;
        }),
      )
      .then((publicURL) => {
        const idx = pendingAttachments.length;
        pendingAttachments.push({ url: publicURL, content_type: file.type, filename: hash });
        chip.dataset.attachmentIdx = idx;
        syncInput();
        chip.classList.add('attachment-chip--done');
        chip.querySelector('.attachment-chip__spinner').remove();
      })
      .catch(() => {
        chip.classList.add('attachment-chip--error');
        const spinner = chip.querySelector('.attachment-chip__spinner');
        if (spinner) spinner.remove();
      })
      .finally(() => {
        URL.revokeObjectURL(objectURL);
        uploadCount--;
        if (uploadCount === 0) setSendDisabled(false);
      });
  }

  // ---- File picker button ----
  const attachBtn = document.querySelector('[data-attach-trigger]');
  if (attachBtn && fileInput) {
    attachBtn.addEventListener('click', () => {
      fileInput.click();
    });
    fileInput.addEventListener('change', () => {
      Array.from(fileInput.files || []).forEach((file) => {
        if (ALLOWED_TYPES[file.type]) uploadFile(file);
      });
      // Reset so selecting the same file again triggers another change event.
      fileInput.value = '';
    });
  }

  // ---- Paste handler ----
  ta.addEventListener('paste', (e) => {
    const items = Array.from(e.clipboardData?.items || []);
    const mediaItems = items.filter((i) => i.kind === 'file' && ALLOWED_TYPES[i.type]);
    if (mediaItems.length === 0) return;

    // Prevent the browser pasting binary data as text into the textarea.
    e.preventDefault();

    mediaItems.forEach((item) => {
      const file = item.getAsFile();
      if (!file) return;
      uploadFile(file);
    });
  });

  // ---- Drag-and-drop handler ----
  const roomMain = document.querySelector('.room-main');
  const overlay = document.getElementById('drop-overlay');
  if (roomMain && overlay) {
    // Track enter/leave depth to avoid flicker when crossing child elements.
    let dragDepth = 0;

    function hasDragFiles(e) {
      const types = e.dataTransfer?.types;
      if (!types) return false;
      return Array.prototype.indexOf.call(types, 'Files') >= 0;
    }

    function showOverlay() {
      overlay.removeAttribute('hidden');
      requestAnimationFrame(() => {
        overlay.classList.add('drop-overlay--active');
      });
    }

    function hideOverlay() {
      overlay.classList.remove('drop-overlay--active');
      overlay.setAttribute('hidden', '');
      dragDepth = 0;
    }

    roomMain.addEventListener('dragenter', (e) => {
      if (!hasDragFiles(e)) return;
      e.preventDefault();
      dragDepth++;
      if (dragDepth === 1) showOverlay();
    });

    roomMain.addEventListener('dragover', (e) => {
      if (!hasDragFiles(e)) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'copy';
    });

    roomMain.addEventListener('dragleave', () => {
      dragDepth--;
      if (dragDepth <= 0) hideOverlay();
    });

    roomMain.addEventListener('drop', (e) => {
      e.preventDefault();
      hideOverlay();

      const files = Array.from(e.dataTransfer?.files || []);
      files.forEach((file) => {
        if (ALLOWED_TYPES[file.type]) uploadFile(file);
      });

      if (ta) ta.focus();
    });
  }

  // Clear attachments when the form resets (fires after successful HTMX send).
  form.addEventListener('reset', () => {
    pendingAttachments = [];
    syncInput();
    previewsEl.innerHTML = '';
    previewsEl.hidden = true;
    setSendDisabled(false);
    uploadCount = 0;
  });
}
