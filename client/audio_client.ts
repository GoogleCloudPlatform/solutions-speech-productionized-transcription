import { io } from "socket.io-client";

const socket = io("ws://localhost:10000/", {});

socket.on("connect", () => {
  console.log(`connect ${socket.id}`);
});

socket.on("disconnect", () => {
  console.log(socket.connected); // false
});

socket.io.on("ping", () => {
  console.log("ping socket"); // false
});
