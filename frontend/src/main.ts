import "@fontsource-variable/inter";
import "./app.css";
// Importing the api first settles which backend is in use (Wails or the
// in-browser mock) before any component asks for data.
import "./lib/api";
import { mount } from "svelte";
import App from "./App.svelte";

mount(App, { target: document.getElementById("app")! });
