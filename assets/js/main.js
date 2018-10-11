var toggler = document.getElementsByClassName("toggler")[0];
var trigger = document.getElementsByClassName("trigger")[0];
var upper = document.getElementsByClassName("upper")[0];
var times = document.getElementsByTagName("time");
var pres = document.getElementsByTagName("pre");

toggler.onclick = function() {
	trigger.style.display = trigger.style.display !== "block" ? "block" : "none";
};

upper.onclick = function() {
	window.scroll(0, 0);
};

window.onresize = function() {
	var clientWidth = document.body.clientWidth;
	if (clientWidth > 600 && trigger.style.display !== "block") {
		trigger.style.display = "block";
	} else if (clientWidth <= 600 && trigger.style.display !== "none") {
		trigger.style.display = "none";
	}

	window.onscroll();
};

window.onscroll = function() {
	var clientWidth = document.body.clientWidth;
	if (clientWidth > 860) {
		upper.style.right = clientWidth / 2 - 440 + "px";
		upper.style.display = window.pageYOffset > 800 ? "block" : "none";
	}
};

for (var i = 0; i < times.length; i++) {
	times[i].innerHTML = moment(times[i].getAttribute("datetime")).format(times[i].getAttribute("format"));
}

for (var i = 0; i < pres.length; i++) {
	hljs.highlightBlock(pres[i].getElementsByTagName("code")[0]);
}
