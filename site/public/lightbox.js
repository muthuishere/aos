// Lightbox for every <a data-zoom href="full-image"> on the page.
// Click or Enter opens the full-size image in a modal <dialog>; Esc, the
// close button, or a click outside the image closes it and returns focus.
(() => {
	const links = document.querySelectorAll('a[data-zoom]');
	if (!links.length) return;
	const dlg = document.createElement('dialog');
	dlg.className = 'lb';
	dlg.innerHTML =
		'<button class="lb-close" type="button" aria-label="Close">&times;</button>' +
		'<figure class="lb-fig"><img class="lb-img" alt="" /><figcaption class="lb-cap"></figcaption></figure>';
	document.body.appendChild(dlg);
	const img = dlg.querySelector('.lb-img');
	const cap = dlg.querySelector('.lb-cap');
	let opener = null;

	const close = () => { if (dlg.open) dlg.close(); };
	dlg.querySelector('.lb-close').addEventListener('click', close);
	dlg.addEventListener('click', (e) => { if (!img.contains(e.target)) close(); });
	dlg.addEventListener('close', () => {
		img.removeAttribute('src');
		if (opener) opener.focus();
	});

	links.forEach((a) => {
		a.addEventListener('click', (e) => {
			e.preventDefault();
			opener = a;
			const thumb = a.querySelector('img');
			img.src = a.href;
			img.alt = thumb ? thumb.alt : '';
			cap.textContent = a.dataset.caption || a.getAttribute('title') || '';
			cap.hidden = !cap.textContent;
			dlg.showModal();
		});
	});
})();
