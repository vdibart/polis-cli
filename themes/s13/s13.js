// S13 Theme JavaScript
// Logo animation and orange toggle functionality

(function() {
    'use strict';

    // Logo Animation
    function initLogoAnimation() {
        const logoContainer = document.getElementById('logoAnimation');
        if (!logoContainer) return;

        const frameCount = 64; // Frames 00-63
        const frameRate = 15; // frames per second
        const duration = (frameCount / frameRate) * 1000; // Total duration in ms
        const frameInterval = duration / frameCount;

        let currentFrame = 0;
        const frames = [];

        // Preload all frames
        for (let i = 0; i < frameCount; i++) {
            const frameNum = i.toString().padStart(2, '0');
            const img = new Image();
            img.src = `animation/LogoAnimation${frameNum}.jpg`;
            frames.push(img);
        }

        // Create img element for animation
        const img = document.createElement('img');
        img.className = 'logo-animation-frame';
        img.src = frames[0].src;
        img.alt = 'Studio13';
        logoContainer.appendChild(img);

        // Animate through frames
        const startTime = Date.now();
        const animate = () => {
            const elapsed = Date.now() - startTime;
            const frameIndex = Math.min(
                Math.floor((elapsed / duration) * frameCount),
                frameCount - 1
            );

            if (frameIndex !== currentFrame && frames[frameIndex]) {
                currentFrame = frameIndex;
                img.src = frames[frameIndex].src;
            }

            if (elapsed < duration) {
                requestAnimationFrame(animate);
            }
        };

        // Start animation
        requestAnimationFrame(animate);
    }

    // Orange Toggle
    function initOrangeToggle() {
        // Load saved preference
        const savedShade = localStorage.getItem('s13-orange-shade') || 'default';
        if (savedShade === 'alt') {
            document.body.classList.add('orange-alt');
        }

        // Toggle button functionality
        const toggleBtn = document.getElementById('orangeToggle');
        if (toggleBtn) {
            toggleBtn.addEventListener('click', function() {
                document.body.classList.toggle('orange-alt');
                const newShade = document.body.classList.contains('orange-alt') ? 'alt' : 'default';
                localStorage.setItem('s13-orange-shade', newShade);
            });
        }
    }

    // Initialize when DOM is ready
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', function() {
            initLogoAnimation();
            initOrangeToggle();
        });
    } else {
        initLogoAnimation();
        initOrangeToggle();
    }
})();
