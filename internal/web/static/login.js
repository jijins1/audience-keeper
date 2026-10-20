"use strict";

const params = new URLSearchParams(location.search);
if (params.has("error")) document.querySelector("#error").classList.remove("hidden");
if (params.get("next")) document.querySelector("#next").value = params.get("next"); // checked server-side
