'use strict';
// Shared browser audio lifecycle for the soundboard and local admin previews.
// Files are requested only when playback is triggered, never preloaded.
(() => {
  function create() {
    const audio = new Audio();
    audio.preload = 'none';
    return audio;
  }
  function stop(audio) {
    audio.pause();
    audio.removeAttribute('src');
    // Do not call load(): this can interrupt pending play() calls in Firefox.
  }
  function play(audio, sound) {
    audio.src = `/api/audio/${encodeURIComponent(sound.filename)}?v=${encodeURIComponent(sound.version)}`;
    return audio.play();
  }
  window.SoundboardAudio = Object.freeze({ create, stop, play });
})();
