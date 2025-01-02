# Kustomize Podinfo Demo Application

## Description

This folder contains a component that has a Kustomize based component in it.
It also contains a Localization, Configuration and Resource objects.

The localization config object can be found in `localization-config.yaml` file.

This file describes how a localization will happen. Where the localized object is and
what file to look for.

Further configuration is provided by the `configuration-config.yaml` file. This sets
the `imagePullPolicy` to `Always`.

## Building the Component

Create a transfer archive:

```
ocm add componentversions --create components.yaml
```

Transport it to the targeted repository:

```
ocm transfer component ./transport-archive ghcr.io/open-component-model/podinfo-kustomize
```
