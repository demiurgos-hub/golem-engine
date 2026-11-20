// Package wire implements the Golem Inspector protocol (inspector.proto): the
// binary envelopes exchanged between in-process agents, the inspector hub and
// browsers, the Schema descriptor golem-bake generates for each game, and a
// game-agnostic entity field store that merges generated State/Delta messages.
//
// The package has no dependency on the golem runtime so both the agent
// (golem/inspector) and the hub (golem/inspector/hub) can share it. Decoders
// return errors instead of panicking because the hub decodes network input.
package wire
