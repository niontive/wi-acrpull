make undeploy
make
make docker-build docker-push IMG=controllerreg.azurecr.io/wi-acrpull:third
make install
make deploy IMG=controllerreg.azurecr.io/wi-acrpull:third
kubectl apply -f ./config/samples/wi-acrpull_v1_wipullbinding.yaml 