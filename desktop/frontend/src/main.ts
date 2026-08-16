import {Events, WML} from "@wailsio/runtime";

// Wire up data-wml-openURL links (logos + footer "Docs" link) once the DOM is ready.
WML.Enable();

// Show the actual Wails version this project was generated against.
document.getElementById('version')!.innerText = "v3.0.0-beta.8";

const timeElement = document.getElementById('time')! as HTMLSpanElement;

Events.On('time', (time) => {
    // The full RFC1123 stamp is too wide for the footer on a phone, so on narrow
    // screens (matching the CSS breakpoint) we show just the clock time.
    const full = time.data;
    const compact = (full.match(/\d{1,2}:\d{2}:\d{2}/) || [full])[0];
    timeElement.innerText = window.matchMedia('(max-width: 640px)').matches ? compact : full;
});
