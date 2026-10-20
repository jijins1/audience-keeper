class Ligne {
    private final String produit;
    private final double prix;
    private final int quantite;

    Ligne(String produit, double prix, int quantite) {
        this.produit = produit;
        this.prix = prix;
        this.quantite = quantite;
    }

    double getPrix() { return prix; }
    int getQuantite() { return quantite; }
}
